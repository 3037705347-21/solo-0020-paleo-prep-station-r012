package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type ReleaseStatus string

const (
	ReleaseActive  ReleaseStatus = "active"
	ReleaseRevoked ReleaseStatus = "revoked"
)

type ReleaseRecord struct {
	ID         string        `json:"id"`
	SpecimenID string        `json:"specimen_id"`
	Purpose    string        `json:"purpose"`
	Recipient  string        `json:"recipient"`
	ApprovedBy string        `json:"approved_by"`
	Status     ReleaseStatus `json:"status"`
	CreatedAt  time.Time     `json:"created_at"`
	// Revocation facts are written once when the release is withdrawn and then
	// never change, so the original release stays fully traceable.
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokedBy    string     `json:"revoked_by,omitempty"`
	RevokeReason string     `json:"revoke_reason,omitempty"`
}

func NewReleaseRecord(
	id string,
	specimen Specimen,
	purpose string,
	recipient string,
	approvedBy string,
	now time.Time,
) (ReleaseRecord, error) {
	if id == "" {
		return ReleaseRecord{}, contracts.Internal("missing_generated_id", "放行标识未生成", nil)
	}
	if !specimen.IsStable() {
		return ReleaseRecord{}, contracts.Conflict("specimen_not_releasable", "只有已稳定标本可以办理放行")
	}
	purposeValue, err := requiredText("借阅目的", purpose, 200)
	if err != nil {
		return ReleaseRecord{}, err
	}
	recipientValue, err := requiredText("接收方", recipient, 120)
	if err != nil {
		return ReleaseRecord{}, err
	}
	approver, err := requiredText("批准人", approvedBy, 80)
	if err != nil {
		return ReleaseRecord{}, err
	}
	return ReleaseRecord{
		ID:         id,
		SpecimenID: specimen.ID,
		Purpose:    purposeValue,
		Recipient:  recipientValue,
		ApprovedBy: approver,
		Status:     ReleaseActive,
		CreatedAt:  now.UTC(),
	}, nil
}

// IsActive reports whether the record is the release that currently makes the
// specimen borrowable.
func (r ReleaseRecord) IsActive() bool {
	return r.Status == ReleaseActive
}

// Revoke is the compensating transition for a release. The record itself is
// kept intact; only its lifecycle status and the revocation facts change.
func (r *ReleaseRecord) Revoke(reason string, revokedBy string, now time.Time) error {
	if r.Status != ReleaseActive {
		return contracts.Conflict("release_already_revoked", "该放行记录已撤销，不能重复撤销")
	}
	reasonValue, err := requiredText("撤销原因", reason, 300)
	if err != nil {
		return err
	}
	operator, err := requiredText("撤销操作人", revokedBy, 80)
	if err != nil {
		return err
	}
	stamp := now.UTC()
	r.Status = ReleaseRevoked
	r.RevokeReason = reasonValue
	r.RevokedBy = operator
	r.RevokedAt = &stamp
	return nil
}
