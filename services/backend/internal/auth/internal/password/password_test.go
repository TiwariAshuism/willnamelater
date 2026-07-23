package password

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	t.Parallel()

	const secret = "correct horse battery staple"

	encoded, err := Hash(secret)
	if err != nil {
		t.Fatalf("Hash: unexpected error: %v", err)
	}
	if !strings.HasPrefix(encoded, "$"+scheme+"$") {
		t.Fatalf("Hash: encoded form %q missing %q prefix", encoded, scheme)
	}

	tests := []struct {
		name      string
		plaintext string
		want      bool
	}{
		{name: "correct password matches", plaintext: secret, want: true},
		{name: "wrong password does not match", plaintext: "wrong password", want: false},
		{name: "empty password does not match", plaintext: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Verify(tc.plaintext, encoded)
			if err != nil {
				t.Fatalf("Verify: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Verify(%q) = %v, want %v", tc.plaintext, got, tc.want)
			}
		})
	}
}

func TestHashUsesFreshSalt(t *testing.T) {
	t.Parallel()

	const secret = "same password twice"
	first, err := Hash(secret)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	second, err := Hash(secret)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if first == second {
		t.Fatal("Hash produced identical output for the same password; salt is not random")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	t.Parallel()

	valid, err := Hash("password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	tests := []struct {
		name    string
		encoded string
	}{
		{name: "empty", encoded: ""},
		{name: "not phc", encoded: "not-a-hash"},
		{name: "wrong scheme", encoded: "$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$a2V5"},
		{name: "wrong version", encoded: "$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$a2V5"},
		{name: "bad params", encoded: "$argon2id$v=19$m=x,t=3,p=2$c2FsdA$a2V5"},
		{name: "bad salt base64", encoded: "$argon2id$v=19$m=65536,t=3,p=2$!!!!$a2V5"},
		{name: "truncated", encoded: valid[:len(valid)-6]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, err := Verify("password", tc.encoded)
			if err == nil {
				t.Fatalf("Verify(%q): expected error, got ok=%v", tc.encoded, ok)
			}
			if ok {
				t.Fatal("Verify: ok must be false on a parse error")
			}
		})
	}
}
