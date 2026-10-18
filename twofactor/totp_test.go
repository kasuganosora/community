package twofactor

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238 Appendix B, SHA1 vectors (8-digit values truncated to 6).
func TestRFC6238Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	cases := map[int64]string{
		59: "94287082", 1111111109: "07081804", 1111111111: "14050471",
		1234567890: "89005924", 2000000000: "69279037", 20000000000: "65353130",
	}
	for ts, want := range cases {
		if got := CodeAt(key, ts/Period); got != want[2:] {
			t.Errorf("t=%d got %s want %s", ts, got, want[2:])
		}
	}
	secret := base32.StdEncoding.EncodeToString(key)
	if c, _ := Generate(secret, time.Unix(59, 0)); c != "287082" {
		t.Fatalf("Generate = %s", c)
	}
}

func TestValidateSkewAndReplay(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	key, _ := DecodeSecret(secret)
	cur := Step(now)
	for _, d := range []int64{-1, 0, 1} {
		if _, ok := Validate(secret, CodeAt(key, cur+d), now, 0); !ok {
			t.Fatalf("skew %d rejected", d)
		}
	}
	for _, d := range []int64{-2, 2} {
		if _, ok := Validate(secret, CodeAt(key, cur+d), now, 0); ok {
			t.Fatalf("skew %d accepted", d)
		}
	}
	step, ok := Validate(secret, CodeAt(key, cur), now, 0)
	if !ok || step != cur {
		t.Fatal("valid code rejected")
	}
	if _, ok := Validate(secret, CodeAt(key, cur), now, step); ok {
		t.Fatal("replayed code accepted")
	}
	if _, ok := Validate(secret, CodeAt(key, cur-1), now, step); ok {
		t.Fatal("older code accepted after newer use")
	}
	if _, ok := Validate(secret, "abc", now, 0); ok {
		t.Fatal("garbage accepted")
	}
}

func TestDecodeSecretLenient(t *testing.T) {
	a, err := NormalizeSecret("jbsw y3dp ehpk 3pxp")
	if err != nil || a != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("%q %v", a, err)
	}
}
