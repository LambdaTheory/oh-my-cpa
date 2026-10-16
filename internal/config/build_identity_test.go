package config

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestBuildIdentityObservations(t *testing.T) {
	revision := strings.Repeat("a1", 20)
	for _, test := range []struct {
		name, revision, ref, tag string
		settings                 []debug.BuildSetting
		want                     BuildIdentity
	}{
		{name: "source checkout", revision: revision, ref: "refs/heads/dev", want: BuildIdentity{"dev+" + revision[:12], revision, "refs/heads/dev"}},
		{name: "explicit tag", revision: revision, tag: "v2.1.0-local", want: BuildIdentity{"v2.1.0-local", revision, ""}},
		{name: "native vcs fallback", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: revision}}, want: BuildIdentity{"dev+" + revision[:12], revision, ""}},
		{name: "unavailable", want: BuildIdentity{}},
		{name: "invalid inputs", revision: "/host/path", ref: "secret\nvalue", tag: "bad flag", want: BuildIdentity{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveBuildIdentity(test.revision, test.ref, test.tag, test.settings); got != test.want {
				t.Fatalf("identity = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestLoadKeepsSourceIdentityIndependentOfRuntimeVersion(t *testing.T) {
	clearConfigEnv(t)
	oldVersion, oldRevision, oldRef, oldTag := BuildVersion, BuildRevision, BuildRef, BuildTag
	t.Cleanup(func() { BuildVersion, BuildRevision, BuildRef, BuildTag = oldVersion, oldRevision, oldRef, oldTag })
	BuildVersion, BuildRevision, BuildRef, BuildTag = "fork", strings.Repeat("b2", 20), "refs/heads/fixture", "v9.8.7"
	t.Setenv("OMCPA_VERSION", "v0.2.6")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "v0.2.6" || cfg.BuildIdentity.Version != "v9.8.7" || cfg.BuildIdentity.Ref != BuildRef || cfg.BuildIdentity.Revision != BuildRevision {
		t.Fatalf("runtime version and source identity were conflated: version=%q identity=%+v", cfg.Version, cfg.BuildIdentity)
	}
	BuildVersion, BuildTag = "v1.2.3", ""
	if identity := readBuildIdentity(); identity.Version != BuildVersion {
		t.Fatalf("existing release injection was not reused: %+v", identity)
	}
	for _, version := range []string{"v0.1.0-dev", "fork", "dev", "unknown", ""} {
		BuildVersion = version
		if identity := readBuildIdentity(); identity.Version != "dev+"+BuildRevision[:12] {
			t.Fatalf("generic version %q replaced observed source identity: %+v", version, identity)
		}
	}
}
