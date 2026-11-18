package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// OnboardingSchema is the version of the onboarding record of decision
// 0045, <project>/.dotai/onboarding.json: the fingerprint of the
// repository that an apply or a dismissal took. dotai writes it, and trusts
// it only to decide whether the session-start hook prints one line, never
// for ownership.
const OnboardingSchema = "dotai.onboarding/v1"

// The sources of an onboarding record.
const (
	OnboardingApplied   = "apply"
	OnboardingDismissed = "dismiss"
)

// Onboarding is the onboarding record: what took it, Source, and the
// fingerprint, its Digest and what it is a digest of: the pack, the names
// of the manifest's profiles, and the sorted names of the profiles with a
// hint that matches a path git tracks, Hinted.
type Onboarding struct {
	Schema   string         `json:"schema"`
	Source   string         `json:"source"`
	Digest   string         `json:"digest"`
	Pack     OnboardingPack `json:"pack"`
	Profiles []string       `json:"profiles"`
	Hinted   []string       `json:"hinted"`
}

// OnboardingPack is the pack of a fingerprint: its identity, such as
// github.com/<owner>/<repo> or local/<name>, and its resolved revision,
// the commit of a git pack or the manifest version of a local one.
type OnboardingPack struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// digestPattern is the SHA-256 in lower-case hex that dotai writes.
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// EncodeOnboarding is the record as indented JSON with a final newline.
func EncodeOnboarding(o Onboarding) ([]byte, error) {
	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeOnboarding reads a record of this OnboardingSchema, from an apply
// or a dismissal, whose digest is a SHA-256 as dotai writes it.
func DecodeOnboarding(data []byte) (Onboarding, error) {
	var o Onboarding
	if err := json.Unmarshal(data, &o); err != nil {
		return Onboarding{}, fmt.Errorf("onboarding record: %w", err)
	}
	if o.Schema != OnboardingSchema {
		return Onboarding{}, fmt.Errorf("onboarding record has schema %q, want %q", o.Schema, OnboardingSchema)
	}
	if o.Source != OnboardingApplied && o.Source != OnboardingDismissed {
		return Onboarding{}, fmt.Errorf("onboarding record has source %q, want %q or %q", o.Source, OnboardingApplied, OnboardingDismissed)
	}
	if !digestPattern.MatchString(o.Digest) {
		return Onboarding{}, errors.New("onboarding record: the digest is not a SHA-256 in lower-case hex")
	}
	for _, list := range []*[]string{&o.Profiles, &o.Hinted} {
		if *list == nil {
			*list = []string{}
		}
	}
	return o, nil
}
