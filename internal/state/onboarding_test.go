package state_test

import (
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/state"
)

func TestOnboardingRecordRoundTrip(t *testing.T) {
	in := state.Onboarding{Schema: state.OnboardingSchema, Source: state.OnboardingApplied, Digest: strings.Repeat("a", 64),
		Pack: state.OnboardingPack{ID: "local/minimal-pack", Revision: "0.1.0"}, Profiles: []string{"web"}, Hinted: []string{}}
	data, err := state.EncodeOnboarding(in)
	if err != nil || !strings.HasSuffix(string(data), "}\n") {
		t.Fatalf("EncodeOnboarding() = %s, %v", data, err)
	}
	out, err := state.DecodeOnboarding(data)
	if err != nil || out.Digest != in.Digest || out.Pack != in.Pack || len(out.Profiles) != 1 || out.Hinted == nil {
		t.Errorf("DecodeOnboarding() = %+v, %v, want %+v", out, err, in)
	}
}

func TestDecodeOnboardingRefusesWhatDotaiNeverWrites(t *testing.T) {
	digest := strings.Repeat("0", 64)
	for name, data := range map[string]string{
		"not JSON":               "{",
		"another schema":         `{"schema":"dotai.onboarding/v9","source":"apply","digest":"` + digest + `"}`,
		"a short digest":         `{"schema":"dotai.onboarding/v1","source":"apply","digest":"abc"}`,
		"a digest in upper case": `{"schema":"dotai.onboarding/v1","source":"apply","digest":"` + strings.ToUpper(strings.Repeat("a", 64)) + `"}`,
		"another source":         `{"schema":"dotai.onboarding/v1","source":"guess","digest":"` + digest + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := state.DecodeOnboarding([]byte(data)); err == nil {
				t.Errorf("DecodeOnboarding(%s) succeeded", data)
			}
		})
	}
}
