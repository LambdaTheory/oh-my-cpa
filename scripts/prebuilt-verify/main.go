// The Docker build uses only the Go standard library to validate supplied assets.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type manifest struct {
	Schema         int                 `json:"schema"`
	SourceRevision string              `json:"source_revision"`
	PreviousDeploy string              `json:"previous_deploy"`
	BuildVersion   string              `json:"build_version"`
	BuildTag       string              `json:"build_tag"`
	SourceFiles    map[string]string   `json:"source_files"`
	SourceSHA256   string              `json:"source_sha256"`
	FrontendSHA256 string              `json:"frontend_sha256"`
	Assets         map[string]string   `json:"assets"`
	AssetsSHA256   string              `json:"assets_sha256"`
	Chunks         map[string][]string `json:"chunks"`
}

func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func digest(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var data strings.Builder
	for _, name := range names {
		fmt.Fprintf(&data, "%s\x00%s\n", name, files[name])
	}
	return hashBytes([]byte(data.String()))
}
func safePath(name string) bool {
	return name != "." && path.Clean(name) == name && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\:\x00\r\n")
}
func isAsset(name string) bool {
	if name == ".gitkeep" || name == "index.html" || name == "favicon.svg" {
		return true
	}
	if !safePath(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	if strings.HasPrefix(name, "lobe-icons/") {
		return path.Ext(name) == ".svg"
	}
	if !strings.HasPrefix(name, "assets/") {
		return false
	}
	switch path.Ext(name) {
	case ".js", ".css", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".gif", ".woff", ".woff2", ".ttf", ".otf":
		return true
	}
	return false
}
func readRegular(root, name string) ([]byte, error) {
	if !safePath(name) {
		return nil, fmt.Errorf("unsafe manifest path: %q", name)
	}
	current := root
	parts := strings.Split(name, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (index == len(parts)-1 && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("nonregular payload: %s", name)
		}
	}
	return os.ReadFile(current)
}

func validate(root string) (*manifest, error) {
	data, err := readRegular(root, "deploy/prebuilt-manifest.json")
	if err != nil {
		return nil, err
	}
	var m manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, err
	}
	shaPattern := regexp.MustCompile(`^[a-f0-9]{40}$`)
	if m.Schema != 1 || !shaPattern.MatchString(m.SourceRevision) || (m.PreviousDeploy != "" && !shaPattern.MatchString(m.PreviousDeploy)) || m.BuildVersion != "fork" || (m.BuildTag != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+@-]{0,199}$`).MatchString(m.BuildTag)) {
		return nil, fmt.Errorf("invalid prebuilt identity")
	}
	if len(m.SourceFiles) == 0 || len(m.Assets) == 0 || digest(m.SourceFiles) != m.SourceSHA256 || digest(m.Assets) != m.AssetsSHA256 {
		return nil, fmt.Errorf("invalid inventory digest")
	}
	frontend := map[string]string{}
	sourceNames := make([]string, 0, len(m.SourceFiles))
	for name := range m.SourceFiles {
		sourceNames = append(sourceNames, name)
	}
	sort.Strings(sourceNames)
	var staleSources []string
	staleCount := 0
	for _, name := range sourceNames {
		// Deployment platforms rewrite compose files in this directory during deployment.
		if strings.HasPrefix(name, "deploy/dokploy/") {
			continue
		}
		expected := m.SourceFiles[name]
		data, err := readRegular(root, name)
		if err != nil {
			return nil, err
		}
		if hashBytes(data) != expected {
			staleCount++
			if len(staleSources) < 20 {
				staleSources = append(staleSources, name)
			}
		}
		if strings.HasPrefix(name, "web/") || strings.HasPrefix(name, "scripts/") || name == "package.json" || strings.HasPrefix(name, "pnpm-") {
			frontend[name] = expected
		}
	}
	if staleCount == 1 {
		return nil, fmt.Errorf("stale source: %s", staleSources[0])
	}
	if staleCount > 1 {
		return nil, fmt.Errorf("stale source (%d of %d): %s", len(staleSources), staleCount, strings.Join(staleSources, ", "))
	}
	if digest(frontend) != m.FrontendSHA256 {
		return nil, fmt.Errorf("stale frontend inputs")
	}
	for _, required := range []string{"web/vite.config.ts", "web/package.json", "pnpm-lock.yaml", "scripts/sync-web-dist.mjs", "go.mod", "Dockerfile.prebuilt"} {
		if m.SourceFiles[required] == "" {
			return nil, fmt.Errorf("missing source input: %s", required)
		}
	}
	// Enumerating the whole input trees catches newly added files that an old manifest never named.
	for _, dir := range []string{"cmd", "internal", "migrations", "scripts", "web"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(filename string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, filename)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(relative)
			if entry.IsDir() {
				if entry.Name() == "node_modules" || name == "web/dist" || name == "internal/web/dist" || name == "web/public/lobe-icons" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(name, ".tsbuildinfo") {
				return nil
			}
			if m.SourceFiles[name] == "" {
				return fmt.Errorf("unexpected source input: %s", name)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	count := 0
	for name := range m.Assets {
		if !isAsset(name) {
			return nil, fmt.Errorf("forbidden asset path: %s", name)
		}
	}
	err = filepath.WalkDir(filepath.Join(root, "internal/web/dist"), func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(filepath.Join(root, "internal/web/dist"), filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		expected, exists := m.Assets[name]
		if !exists {
			return fmt.Errorf("unexpected asset: %s", name)
		}
		data, err := readRegular(root, "internal/web/dist/"+name)
		if err != nil {
			return err
		}
		if hashBytes(data) != expected {
			return fmt.Errorf("modified asset: %s", name)
		}
		count++
		return nil
	})
	if err != nil {
		return nil, err
	}
	if count != len(m.Assets) {
		return nil, fmt.Errorf("missing assets")
	}
	if m.Assets["index.html"] == "" {
		return nil, fmt.Errorf("missing SPA entry")
	}
	// Vite copies public resources verbatim; this covers font shards and local artwork.
	for name, expected := range m.SourceFiles {
		if strings.HasPrefix(name, "web/public/") && m.Assets[strings.TrimPrefix(name, "web/public/")] != expected {
			return nil, fmt.Errorf("missing or changed public resource: %s", name)
		}
	}
	catalogBytes, err := readRegular(root, "web/src/generated/lobeIconCatalog.json")
	if err != nil {
		return nil, err
	}
	var catalog []struct {
		ID       string `json:"id"`
		HasColor bool   `json:"hasColor"`
	}
	if err := json.Unmarshal(catalogBytes, &catalog); err != nil {
		return nil, err
	}
	for _, icon := range catalog {
		slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(icon.ID), "-"), "-")
		names := []string{slug + ".svg"}
		if icon.HasColor {
			names = append(names, slug+"-color.svg")
		}
		for _, name := range names {
			if m.Assets["lobe-icons/"+name] == "" {
				return nil, fmt.Errorf("missing provider artwork: %s", name)
			}
		}
	}
	if len(m.Chunks) == 0 {
		return nil, fmt.Errorf("missing bundle dependency graph")
	}
	for file, references := range m.Chunks {
		if m.Assets[file] == "" || path.Ext(file) != ".js" {
			return nil, fmt.Errorf("missing graph chunk: %s", file)
		}
		for _, ref := range references {
			if !safePath(ref) || m.Assets[ref] == "" {
				return nil, fmt.Errorf("missing graph dependency: %s", ref)
			}
		}
	}
	if err := validateReferences(root, m.Assets); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateReferences(root string, assets map[string]string) error {
	htmlRefs := regexp.MustCompile(`(?i)<(?:script|link|img)\b[^>]*?\b(?:src|href)\s*=\s*["']([^"']+)["']`)
	cssRefs := regexp.MustCompile(`(?i)url\(\s*["']?([^"')\s]+)["']?\s*\)|@import\s+["']([^"']+)["']`)
	for name := range assets {
		extension := path.Ext(name)
		if extension != ".html" && extension != ".css" {
			continue
		}
		data, err := readRegular(root, "internal/web/dist/"+name)
		if err != nil {
			return err
		}
		pattern := htmlRefs
		if extension == ".html" {
			pattern = htmlRefs
		}
		if extension == ".css" {
			pattern = cssRefs
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			ref := match[1]
			if ref == "" && len(match) > 2 {
				ref = match[2]
			}
			if strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "#") {
				continue
			}
			if strings.Contains(ref, ":") || strings.HasPrefix(ref, "/") || strings.Contains(ref, "\\") {
				return fmt.Errorf("nonrelative asset reference in %s: %s", name, ref)
			}
			ref = strings.Split(strings.Split(ref, "?")[0], "#")[0]
			target := path.Join(path.Dir(name), ref)
			if !safePath(target) || assets[target] == "" {
				return fmt.Errorf("missing referenced asset in %s: %s", name, ref)
			}
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: prebuilt-verify <root>")
		os.Exit(1)
	}
	m, err := validate(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	const prefix = "github.com/oh-my-cpa/oh-my-cpa/internal/config."
	fmt.Printf("-X %sBuildVersion=fork -X %sBuildRevision=%s", prefix, prefix, m.SourceRevision)
	if m.BuildTag != "" {
		fmt.Printf(" -X %sBuildTag=%s", prefix, m.BuildTag)
	}
}
