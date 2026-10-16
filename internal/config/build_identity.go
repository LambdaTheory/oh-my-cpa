package config

import (
	"regexp"
	"runtime/debug"
)

// These linker inputs describe source, independently of the operator's OMCPA_VERSION.
var BuildRevision, BuildRef, BuildTag string

type BuildIdentity struct {
	Version  string
	Revision string
	Ref      string
}

var buildRevisionPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
var buildLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+@-]{0,199}$`)

func resolveBuildIdentity(revision, ref, tag string, settings []debug.BuildSetting) BuildIdentity {
	if !buildRevisionPattern.MatchString(revision) {
		revision = ""
		for _, setting := range settings {
			if setting.Key == "vcs.revision" && buildRevisionPattern.MatchString(setting.Value) {
				revision = setting.Value
			}
		}
	}
	if !buildLabelPattern.MatchString(ref) {
		ref = ""
	}
	identity := BuildIdentity{Revision: revision, Ref: ref}
	if buildLabelPattern.MatchString(tag) {
		identity.Version = tag
	} else if revision != "" {
		identity.Version = "dev+" + revision[:12]
	}
	return identity
}

func readBuildIdentity() BuildIdentity {
	var settings []debug.BuildSetting
	if info, hasInfo := debug.ReadBuildInfo(); hasInfo {
		settings = info.Settings
	}
	tag := BuildTag
	if tag == "" && BuildVersion != "v0.1.0-dev" && BuildVersion != "fork" && BuildVersion != "dev" && BuildVersion != "unknown" {
		tag = BuildVersion
	}
	return resolveBuildIdentity(BuildRevision, BuildRef, tag, settings)
}
