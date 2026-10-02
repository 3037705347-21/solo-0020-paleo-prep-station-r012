package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type ReleaseRecord struct {
	ID         string    `json:"id"`
	SpecimenID string    `json:"specimen_id"`
	Purpose    string    `json:"purpose"`
	Recipient  string    `json:"recipient"`
	ApprovedBy string    `json:"approved_by"`
	CreatedAt  time.Time `json:"created_at"`

	// 撤销事实只在原记录上追加，记录本身永不删除，以保留完整追溯轨迹。
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	RevokedBy string     `json:"revoked_by,omitempty"`
	Reason    string     `json:"reason,omitempty"`
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
		CreatedAt:  now.UTC(),
	}, nil
}

// IsCurrent 表示该放行仍然有效，尚未被撤销。
func (r ReleaseRecord) IsCurrent() bool {
	return r.RevokedAt == nil
}

// Revoke 在保留原放行事实的前提下追加撤销原因、操作人和时间。
// 已撤销的记录不能再次撤销。
func (r *ReleaseRecord) Revoke(reason string, revokedBy string, now time.Time) error {
	if !r.IsCurrent() {
		return contracts.Conflict("release_already_revoked", "该放行已经撤销，不能重复撤销")
	}
	reasonValue, err := requiredText("撤销原因", reason, 200)
	if err != nil {
		return err
	}
	operator, err := requiredText("撤销操作人", revokedBy, 80)
	if err != nil {
		return err
	}
	stamp := now.UTC()
	r.RevokedAt = &stamp
	r.RevokedBy = operator
	r.Reason = reasonValue
	return nil
}
