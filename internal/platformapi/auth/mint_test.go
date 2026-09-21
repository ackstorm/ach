// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestMintPK_OAuthPurposeRowAndDiscardablePlaintext(t *testing.T) {
	flm := newFakeLiteLLM()
	rec := newDBInsertRecord()
	deps := Deps{LiteLLM: flm, Pepper: []byte("test-pepper-32-bytes-long-aaaaaa"),
		KeyEncryptionKey: testDEK(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InsertPKFn: rec.insertFn, Issuer: "https://ach.test"}

	plaintext, row, err := deps.MintPK(context.Background(), "u@x.com", "litellm-user-1", "oauth")
	if err != nil {
		t.Fatal(err)
	}
	if md := flm.rec.lastKeyGenerateReq.Metadata; md["ach_issuer"] != "https://ach.test" || md["ach_key_id"] != row.KeyID {
		t.Fatalf("key metadata: %v", md)
	}
	if plaintext == "" || row.Purpose != "oauth" || row.OwnerEmail != "u@x.com" || row.LiteLLMToken == nil {
		t.Fatalf("plaintext=%q row=%+v", plaintext, row)
	}
	if rec.calls != 1 || rec.lastRow.KeyID != row.KeyID || rec.lastRow.Purpose != "oauth" {
		t.Fatalf("insert not recorded: %+v", rec.lastRow)
	}
}

func TestMintPK_InsertFailureCompensatesAndClassifies(t *testing.T) {
	flm := newFakeLiteLLM()
	revoked := 0
	flm.revokeKeyError = func(string) error { revoked++; return nil }
	rec := newDBInsertRecord()
	rec.failWith = errors.New("db down")
	deps := Deps{LiteLLM: flm, Pepper: []byte("test-pepper-32-bytes-long-aaaaaa"),
		KeyEncryptionKey: testDEK(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InsertPKFn: rec.insertFn}
	_, _, err := deps.MintPK(context.Background(), "u@x.com", "uid", "cli")
	var me *MintError
	if !errors.As(err, &me) || me.Status != 500 || me.KeyID == "" || revoked != 1 {
		t.Fatalf("err=%v revoked=%d", err, revoked)
	}
}
