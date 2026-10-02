package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) ReleaseSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.ReleaseRequest,
) (domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReleaseRecord{}, err
	}

	var created domain.ReleaseRecord
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_blocks_release", "存在未结束方案，不能办理研究放行")
		}
		if _, exists := m.ActiveReleaseForSpecimen(specimenID); exists {
			return contracts.Conflict("release_exists", "该标本已存在有效放行记录")
		}
		record, err := domain.NewReleaseRecord(
			s.nextID("release"),
			specimen,
			request.Purpose,
			request.Recipient,
			request.ApprovedBy,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := specimen.MarkAvailable(s.now()); err != nil {
			return err
		}
		m.PutRelease(record)
		m.PutSpecimen(specimen)
		created = record
		return nil
	})
	return created, err
}

// RevokeRelease withdraws the latest valid release of a specimen. The original
// record is retained with its reason, operator and timestamp; the specimen
// leaves available and becomes stabilized so it cannot be treated as
// borrowable. Every failing check runs against the transaction copy and
// returns without committing any entity change.
func (s *Service) RevokeRelease(
	ctx context.Context,
	specimenID string,
	request contracts.ReleaseRevocationRequest,
) (domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReleaseRecord{}, err
	}

	var revoked domain.ReleaseRecord
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_blocks_revocation", "存在未结束方案，不能撤销研究放行")
		}
		history := m.ReleasesForSpecimen(specimenID)
		if len(history) == 0 {
			return contracts.Conflict("release_missing", "该标本不存在放行记录，不能撤销")
		}
		latest := history[len(history)-1]

		if request.ReleaseID != "" && request.ReleaseID != latest.ID {
			// A release that does not exist or belongs to another specimen is
			// not part of this specimen's history at all.
			referenced, found := m.GetRelease(request.ReleaseID)
			if !found || referenced.SpecimenID != specimenID {
				return notFoundRelease(request.ReleaseID)
			}
			return contracts.Conflict("release_not_current", "只能撤销最新的放行记录")
		}
		if !latest.IsActive() {
			return contracts.Conflict("release_already_revoked", "最新放行记录已经撤销，不能重复撤销")
		}
		if err := latest.Revoke(request.Reason, request.RevokedBy, s.now()); err != nil {
			return err
		}
		if err := specimen.RevokeAvailable(s.now()); err != nil {
			return err
		}
		m.PutRelease(latest)
		m.PutSpecimen(specimen)
		revoked = latest
		return nil
	})
	return revoked, err
}

func (s *Service) ListReleasesForSpecimen(
	ctx context.Context,
	specimenID string,
) ([]domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.ReleaseRecord
	err := s.repo.Read(func(m *repository.Mutation) error {
		if _, ok := m.GetSpecimen(specimenID); !ok {
			return notFoundSpecimen(specimenID)
		}
		values = m.ReleasesForSpecimen(specimenID)
		return nil
	})
	return values, err
}
