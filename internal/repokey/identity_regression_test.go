package repokey

import "testing"

func TestRepoDisplayPreservesGitHubSpellingWithoutURLCredentials(t *testing.T) {
	for _, input := range []string{"T14-Fixture/Case-Repo", "https://synthetic-user:synthetic-password@github.com/T14-Fixture/Case-Repo.git", "git@github.com:T14-Fixture/Case-Repo.git"} {
		display, err := RepoDisplay(input)
		if err != nil || display != "T14-Fixture/Case-Repo" {
			t.Fatalf("submitted repository spelling was not safely retained: display=%q err=%v", display, err)
		}
	}
}

func TestNormalizeRepoGitHubCaseIdentity(t *testing.T) {
	for _, input := range []string{"https://github.com/T14-Fixture/Case-Repo.git", "git@github.com:t14-fixture/case-repo.git", "T14-Fixture/Case-Repo", "ssh://git@GITHUB.COM/T14-FIXTURE/CASE-REPO.git"} {
		got, hasRemote, err := NormalizeRepo(input)
		if err != nil || !hasRemote || got != "t14-fixture/case-repo" {
			t.Errorf("%q: identity=%q remote=%t err=%v", input, got, hasRemote, err)
		}
	}
}

func TestNormalizeRepoKeepsOtherHostsAndDirectoriesCaseSensitive(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://gitlab.com/Team/Project.git", "https://gitlab.com/team/project.git"},
		{"gitlab.com/Team/Project", "gitlab.com/team/project"},
		{"https://git.example.com/Repo", "https://git.example.com/repo"},
		{"git.example.com/Repo", "git.example.com/repo"},
		{"http://localhost/Repo", "http://localhost/repo"},
		{"localhost/Repo", "localhost/repo"},
		{"ssh://git@example.test/Team/Project.git", "ssh://git@example.test/team/project.git"},
		{"E:/works/My-Local-Project", "E:/works/my-local-project"},
		{"My-Local-Project", "my-local-project"},
	} {
		one, _, err := NormalizeRepo(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		two, _, err := NormalizeRepo(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if one == two {
			t.Errorf("case-sensitive identities were merged: %q / %q", pair[0], pair[1])
		}
	}
}
