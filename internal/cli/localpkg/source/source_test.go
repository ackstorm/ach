// SPDX-License-Identifier: Apache-2.0
package source

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name, ref, auth string
		want            SourceURI
		wantErr         bool
	}{
		{"github bare", "github:obra/superpowers", "", SourceURI{Kind: KindGitHub, CloneURL: "https://github.com/obra/superpowers.git", AuthScheme: AuthBearer}, false},
		{"github ref", "github:obra/superpowers#main", "", SourceURI{Kind: KindGitHub, CloneURL: "https://github.com/obra/superpowers.git", GitRef: "main", AuthScheme: AuthBearer}, false},
		{"github dotgit", "github:obra/superpowers.git", "", SourceURI{Kind: KindGitHub, CloneURL: "https://github.com/obra/superpowers.git", AuthScheme: AuthBearer}, false},
		{"gitlab self-hosted infers oauth2", "git:https://git.ackstorm.com/grp/repo.git#v1", "", SourceURI{Kind: KindGit, CloneURL: "https://git.ackstorm.com/grp/repo.git", GitRef: "v1", AuthScheme: AuthBasicOAuth2}, false},
		{"git generic bearer", "git:https://example.com/x/y.git", "", SourceURI{Kind: KindGit, CloneURL: "https://example.com/x/y.git", AuthScheme: AuthBearer}, false},
		{"auth override wins", "git:https://example.com/x/y.git", "basic-oauth2", SourceURI{Kind: KindGit, CloneURL: "https://example.com/x/y.git", AuthScheme: AuthBasicOAuth2}, false},
		{"empty", "", "", SourceURI{}, true},
		{"unknown scheme", "ftp://x", "", SourceURI{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.ref, tc.auth)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestParseLocal(t *testing.T) {
	got, err := Parse("./fixtures/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindLocal || got.LocalPath == "" || got.LocalPath[0] != '/' {
		t.Fatalf("local parse wrong: %+v", got)
	}
}
