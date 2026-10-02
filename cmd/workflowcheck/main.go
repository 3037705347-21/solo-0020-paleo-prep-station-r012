package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	Status        string `json:"status"`
}

type planView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Status     string `json:"status"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID           string     `json:"id"`
	SpecimenID   string     `json:"specimen_id"`
	Status       string     `json:"status"`
	Purpose      string     `json:"purpose"`
	Recipient    string     `json:"recipient"`
	ApprovedBy   string     `json:"approved_by"`
	RevokedBy    string     `json:"revoked_by"`
	RevokeReason string     `json:"revoke_reason"`
	RevokedAt    *time.Time `json:"revoked_at"`
}

type releaseHistory struct {
	Items []releaseView `json:"items"`
}

type errorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil
	case "revoke-release":
		return checkRevokeRelease(client, server.URL)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

// checkRevokeRelease exercises the compensating release lifecycle: revoke the
// current release with full traceability, reject every illegal revocation, and
// require a fresh stabilization cycle before re-release.
func checkRevokeRelease(client *http.Client, baseURL string) error {
	specimen, plan, err := completedPlanSetup(client, baseURL)
	if err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, plan.ID, 100, "表面加固"); err != nil {
		return err
	}

	var release releaseView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "比较解剖研究",
			Recipient:  "地层研究组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusCreated,
		&release,
	); err != nil {
		return err
	}

	// Unknown specimen is a 404 and changes nothing.
	if body, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/specimen-missing/release/revocation",
		contracts.ReleaseRevocationRequest{Reason: "x", RevokedBy: "keeper-zhou"},
		http.StatusNotFound,
	); err != nil {
		return err
	} else if body.Code != "specimen_not_found" {
		return fmt.Errorf("expected specimen_not_found, got %s", body.Code)
	}

	// Non-existent and foreign release ids are 404.
	if _, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release/revocation",
		contracts.ReleaseRevocationRequest{
			ReleaseID: "release-missing",
			Reason:    "接收方取消借阅",
			RevokedBy: "keeper-zhou",
		},
		http.StatusNotFound,
	); err != nil {
		return err
	}

	other, otherPlan, err := completedPlanSetupOther(client, baseURL, "PPS-2026-002")
	if err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, otherPlan.ID, 100, "表面加固"); err != nil {
		return err
	}
	var otherRelease releaseView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+other.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "另一项研究",
			Recipient:  "古生态组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusCreated,
		&otherRelease,
	); err != nil {
		return err
	}
	if _, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release/revocation",
		contracts.ReleaseRevocationRequest{
			ReleaseID: otherRelease.ID,
			Reason:    "接收方取消借阅",
			RevokedBy: "keeper-zhou",
		},
		http.StatusNotFound,
	); err != nil {
		return err
	}

	// Missing reason or operator is a 400.
	if _, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release/revocation",
		contracts.ReleaseRevocationRequest{RevokedBy: "keeper-zhou"},
		http.StatusBadRequest,
	); err != nil {
		return err
	}

	// The valid revocation: specimen leaves available and history is retained.
	var revoked releaseView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release/revocation",
		contracts.ReleaseRevocationRequest{
			ReleaseID: release.ID,
			Reason:    "接收方取消借阅，运输中包装受损",
			RevokedBy: "keeper-zhou",
		},
		http.StatusOK,
		&revoked,
	); err != nil {
		return err
	}
	if revoked.Status != "revoked" || revoked.RevokedBy != "keeper-zhou" ||
		revoked.RevokeReason == "" || revoked.RevokedAt == nil {
		return errors.New("revocation did not record status, reason, operator and time")
	}
	if revoked.ApprovedBy != "keeper-zhou" || revoked.Purpose != "比较解剖研究" {
		return errors.New("original release facts were altered by revocation")
	}

	var fetched specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Status != "stabilized" {
		return fmt.Errorf("expected revoked specimen to be stabilized, got %s", fetched.Status)
	}

	// The specimen is no longer borrowable: a second release is still possible
	// only through the regular release conditions — but duplicate revocation is
	// rejected and nothing changes.
	if body, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release/revocation",
		contracts.ReleaseRevocationRequest{
			ReleaseID: release.ID,
			Reason:    "重复撤销",
			RevokedBy: "keeper-zhou",
		},
		http.StatusConflict,
	); err != nil {
		return err
	} else if body.Code != "release_already_revoked" {
		return fmt.Errorf("expected release_already_revoked, got %s", body.Code)
	}

	var history releaseHistory
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID+"/releases", nil, http.StatusOK, &history); err != nil {
		return err
	}
	if len(history.Items) != 1 || history.Items[0].ID != release.ID || history.Items[0].Status != "revoked" {
		return errors.New("release history is missing the original revoked record")
	}

	// Revocation while an unfinished rework plan exists is blocked.
	var rework planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "修复运输造成的裂隙",
			Method:         "黏接加固",
			Adhesive:       "可逆树脂",
			TargetExposure: 90,
		},
		http.StatusCreated,
		&rework,
	); err != nil {
		return err
	}
	if body, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release/revocation",
		contracts.ReleaseRevocationRequest{
			Reason:    "方案进行中尝试撤销",
			RevokedBy: "keeper-zhou",
		},
		http.StatusConflict,
	); err != nil {
		return err
	} else if body.Code != "open_plan_blocks_revocation" {
		return fmt.Errorf("expected open_plan_blocks_revocation, got %s", body.Code)
	}

	// Finish the new repair round; re-release must re-satisfy stability and a
	// closed plan with still only one active release.
	var approvedRework planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+rework.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approvedRework,
	); err != nil {
		return err
	}
	if approvedRework.Status != "approved" {
		return fmt.Errorf("expected approved rework plan, got %s", approvedRework.Status)
	}
	if err := recordProgress(client, baseURL, rework.ID, 100, "裂隙加固完成"); err != nil {
		return err
	}
	var secondRelease releaseView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "修复后的形态对比研究",
			Recipient:  "比较解剖组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusCreated,
		&secondRelease,
	); err != nil {
		return err
	}
	if secondRelease.ID == release.ID || secondRelease.Status != "active" {
		return errors.New("re-release must create a new active record")
	}
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Status != "available" {
		return fmt.Errorf("expected re-released specimen available, got %s", fetched.Status)
	}
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID+"/releases", nil, http.StatusOK, &history); err != nil {
		return err
	}
	if len(history.Items) != 2 || history.Items[0].Status != "revoked" || history.Items[1].Status != "active" {
		return errors.New("full release trail (revoked then active) is not preserved")
	}

	// Revoking the old (non-current) record is rejected, only the latest wins.
	if body, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release/revocation",
		contracts.ReleaseRevocationRequest{
			ReleaseID: release.ID,
			Reason:    "撤销旧放行",
			RevokedBy: "keeper-zhou",
		},
		http.StatusConflict,
	); err != nil {
		return err
	} else if body.Code != "release_not_current" {
		return fmt.Errorf("expected release_not_current, got %s", body.Code)
	}

	// A stabilized specimen that never had a release cannot start a rework plan.
	plain, plainPlan, err := completedPlanSetupOther(client, baseURL, "PPS-2026-003")
	if err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, plainPlan.ID, 100, "表面加固"); err != nil {
		return err
	}
	if body, err := expectFailure(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+plain.ID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "无放行历史的再制备",
			Method:         "手工处理",
			TargetExposure: 80,
		},
		http.StatusConflict,
	); err != nil {
		return err
	} else if body.Code != "specimen_not_reworkable" {
		return fmt.Errorf("expected specimen_not_reworkable, got %s", body.Code)
	}
	return nil
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	return registerSpecimenWithCatalog(client, baseURL, "PPS-2026-001")
}

func registerSpecimenWithCatalog(client *http.Client, baseURL, catalog string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: catalog,
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

// completedPlanSetupOther registers a second specimen with its own catalog
// number so checks can reference a release that belongs to another specimen.
func completedPlanSetupOther(client *http.Client, baseURL, catalog string) (specimenView, planView, error) {
	specimen, err := registerSpecimenWithCatalog(client, baseURL, catalog)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

// expectFailure asserts that a request fails with the given status and returns
// the decoded stable error body. It is used for the revocation boundaries that
// must leave entity state untouched.
func expectFailure(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
) (errorView, error) {
	var view errorView
	err := requestJSON(client, method, url, body, wantStatus, &view)
	return view, err
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
