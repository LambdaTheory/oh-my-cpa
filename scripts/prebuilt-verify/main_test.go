package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, root, name, value string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (string, *manifest) {
	t.Helper()
	root := t.TempDir()
	source := map[string]string{"web/vite.config.ts": "relative base", "web/package.json": "{}", "pnpm-lock.yaml": "lock",
		"scripts/sync-web-dist.mjs": "sync", "go.mod": "module example", "Dockerfile.prebuilt": "FROM golang",
		"web/src/generated/lobeIconCatalog.json": `[{"id":"Example","hasColor":true}]`, "web/public/favicon.svg": "icon",
		"cmd/main.go": "package main", "migrations/001.sql": "select 1"}
	assets := map[string]string{".gitkeep": "", "index.html": `<link href="./favicon.svg"><script type="module" src="./assets/main.js"></script>`,
		"assets/main.js": `import "./lazy.js";`, "assets/lazy.js": `export default 1`,
		"assets/main.css": `@font-face{src:url(./font.woff2)}`, "assets/font.woff2": "font",
		"favicon.svg": "icon", "lobe-icons/example.svg": "icon", "lobe-icons/example-color.svg": "color"}
	m := &manifest{Schema: 1, SourceRevision: strings.Repeat("a", 40), BuildVersion: "fork", SourceFiles: map[string]string{}, Assets: map[string]string{}}
	for name, value := range source {
		writeFixture(t, root, name, value)
		m.SourceFiles[name] = hashBytes([]byte(value))
	}
	for name, value := range assets {
		writeFixture(t, root, "internal/web/dist/"+name, value)
		m.Assets[name] = hashBytes([]byte(value))
	}
	m.Chunks = map[string][]string{"assets/main.js": {"assets/lazy.js", "assets/main.css"}, "assets/lazy.js": {}}
	saveManifest(t, root, m)
	return root, m
}
func saveManifest(t *testing.T, root string, m *manifest) {
	t.Helper()
	frontend := map[string]string{}
	for name, value := range m.SourceFiles {
		if strings.HasPrefix(name, "web/") || strings.HasPrefix(name, "scripts/") || name == "package.json" || strings.HasPrefix(name, "pnpm-") {
			frontend[name] = value
		}
	}
	m.SourceSHA256 = digest(m.SourceFiles)
	m.FrontendSHA256 = digest(frontend)
	m.AssetsSHA256 = digest(m.Assets)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "deploy/prebuilt-manifest.json", string(data))
}
func TestValidatePayload(t *testing.T) {
	root, m := fixture(t)
	got, err := validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceRevision != m.SourceRevision {
		t.Fatal("identity changed")
	}
	cases := []struct {
		name   string
		mutate func(string, *manifest)
	}{
		{"missing manifest", func(root string, m *manifest) { os.Remove(filepath.Join(root, "deploy/prebuilt-manifest.json")) }},
		{"missing asset", func(root string, m *manifest) { os.Remove(filepath.Join(root, "internal/web/dist/assets/lazy.js")) }},
		{"modified asset", func(root string, m *manifest) { writeFixture(t, root, "internal/web/dist/assets/main.js", "tampered") }},
		{"unexpected asset", func(root string, m *manifest) { writeFixture(t, root, "internal/web/dist/extra.js", "extra") }},
		{"stale source", func(root string, m *manifest) { writeFixture(t, root, "web/vite.config.ts", "changed") }},
		{"added source", func(root string, m *manifest) { writeFixture(t, root, "web/src/new.ts", "new") }},
		{"missing index", func(root string, m *manifest) {
			delete(m.Assets, "index.html")
			os.Remove(filepath.Join(root, "internal/web/dist/index.html"))
			saveManifest(t, root, m)
		}},
		{"missing public resource", func(root string, m *manifest) {
			delete(m.Assets, "favicon.svg")
			os.Remove(filepath.Join(root, "internal/web/dist/favicon.svg"))
			saveManifest(t, root, m)
		}},
		{"missing provider color", func(root string, m *manifest) {
			delete(m.Assets, "lobe-icons/example-color.svg")
			os.Remove(filepath.Join(root, "internal/web/dist/lobe-icons/example-color.svg"))
			saveManifest(t, root, m)
		}},
		{"unsafe identity", func(root string, m *manifest) { m.BuildTag = "tag -X injected"; saveManifest(t, root, m) }},
		{"unsafe path", func(root string, m *manifest) { m.SourceFiles["../outside"] = hashBytes(nil); saveManifest(t, root, m) }},
		{"wrong digest", func(root string, m *manifest) {
			m.AssetsSHA256 = "wrong"
			data, _ := json.Marshal(m)
			writeFixture(t, root, "deploy/prebuilt-manifest.json", string(data))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, m := fixture(t)
			tc.mutate(root, m)
			if _, err := validate(root); err == nil {
				t.Fatal("accepted invalid payload")
			}
		})
	}
}
func TestOfflineRelativeReferences(t *testing.T) {
	for _, ref := range []string{"https://cdn.invalid/a.js", "/assets/lazy.js", "assets/missing.js", "../outside.js"} {
		t.Run(ref, func(t *testing.T) {
			root, m := fixture(t)
			m.Chunks["assets/main.js"] = []string{ref}
			saveManifest(t, root, m)
			if _, err := validate(root); err == nil {
				t.Fatal("accepted external or missing graph dependency")
			}
		})
	}
	t.Run("minified string is not an import", func(t *testing.T) {
		root, m := fixture(t)
		content := `const text='from"unexpected"';`
		writeFixture(t, root, "internal/web/dist/assets/main.js", content)
		m.Assets["assets/main.js"] = hashBytes([]byte(content))
		saveManifest(t, root, m)
		if _, err := validate(root); err != nil {
			t.Fatal(err)
		}
	})
	for _, content := range []string{`<script src="https://cdn.invalid/a.js"></script>`, `<script src="/assets/main.js"></script>`, `<link href="./missing.css">`} {
		t.Run(content, func(t *testing.T) {
			root, m := fixture(t)
			writeFixture(t, root, "internal/web/dist/index.html", content)
			m.Assets["index.html"] = hashBytes([]byte(content))
			saveManifest(t, root, m)
			if _, err := validate(root); err == nil {
				t.Fatal("accepted invalid HTML reference")
			}
		})
	}
	for _, content := range []string{`@import "https://cdn.invalid/style.css";`, `body{background:url(/image.png)}`, `@font-face{src:url(./missing.woff2)}`} {
		t.Run(content, func(t *testing.T) {
			root, m := fixture(t)
			writeFixture(t, root, "internal/web/dist/assets/main.css", content)
			m.Assets["assets/main.css"] = hashBytes([]byte(content))
			saveManifest(t, root, m)
			if _, err := validate(root); err == nil {
				t.Fatal("accepted invalid CSS reference")
			}
		})
	}
}
