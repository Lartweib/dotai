package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/lartweib/dotai/internal/app"
)

// The confirm of an action (decision 0043): a plan or a preview carries
// the digest of what it shows, and the action that carries it out takes
// that digest back. The use case first completes the recovery of an
// earlier operation that stopped midway, as the command line does, then
// plans and asks Confirm with the plan it is about to carry out, before it
// writes anything of the plan; an action whose plan has another digest
// stops there with CodePlanChanged and the new plan, so that the user
// carries out only a plan they saw.

// digest returns the digest of v: "sha256:" and the SHA-256 in hex of its
// JSON, as the API sends it. Two plans with the same JSON have the same
// digest.
func digest(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// The plans and previews as the API sends them: the data of the command,
// with the digest of that data and whether the page can carry it out.
type (
	installPlanView struct {
		app.InstallPlan
		Digest string `json:"digest"`
	}
	projectPlanView struct {
		app.ProjectPlan
		Digest string `json:"digest"`
	}
	restoreView struct {
		app.RestoreResult
		Digest string `json:"digest"`
	}
	projectRestoreView struct {
		app.ProjectRestoreResult
		Digest string `json:"digest"`
	}
	doctorView struct {
		app.DoctorResult
		Digest string `json:"digest"`
	}
	packUpdateView struct {
		app.PackUpdatePlan
		Digest string `json:"digest"`
	}
)

func installView(p app.InstallPlan) (any, error) {
	d, err := digest(p)
	return installPlanView{p, d}, err
}

func projectView(p app.ProjectPlan) (any, error) {
	d, err := digest(p)
	return projectPlanView{p, d}, err
}

func restorePreview(r app.RestoreResult) (any, error) {
	d, err := digest(r)
	return restoreView{r, d}, err
}

func projectRestorePreview(r app.ProjectRestoreResult) (any, error) {
	d, err := digest(r)
	return projectRestoreView{r, d}, err
}

func doctorFindings(r app.DoctorResult) (any, error) {
	d, err := digest(r)
	return doctorView{r, d}, err
}

func updateView(p app.PackUpdatePlan) (any, error) {
	d, err := digest(p)
	return packUpdateView{p, d}, err
}

// confirm returns the Confirm of an action whose user saw the plan of the
// digest want: it refuses any other plan with CodePlanChanged and the new
// plan as view gives it, with its own digest, in the envelope's data.
func confirm[T any](want string, view func(T) (any, error)) func(T) error {
	return func(v T) error {
		got, err := digest(v)
		if err != nil {
			return err
		}
		if got == want {
			return nil
		}
		data, err := view(v)
		if err != nil {
			return err
		}
		return &apiError{code: CodePlanChanged, status: http.StatusConflict, data: data,
			msg: "the plan changed since it was shown, so the action stopped before writing it: review the new plan and confirm it again"}
	}
}
