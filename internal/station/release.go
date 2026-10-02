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
		if _, exists := m.CurrentReleaseForSpecimen(specimenID); exists {
			return contracts.Conflict("release_exists", "该标本已经存在有效放行记录")
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

// RevokeRelease 撤销标本的当前有效放行。原放行记录保留并追加撤销事实，
// 标本补偿回到 stabilized，不再可借阅。任何前置条件不满足都直接失败，
// 由于操作发生在仓储副本上，失败不会改变已提交的实体状态。
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
		record, ok := m.CurrentReleaseForSpecimen(specimenID)
		if !ok {
			return contracts.Conflict("release_not_current", "该标本不存在可撤销的当前有效放行")
		}
		// 指定的放行标识必须就是当前有效放行，旧放行（已撤销）一律拒绝。
		if request.ReleaseID != "" && request.ReleaseID != record.ID {
			return contracts.Conflict("release_not_current", "只能撤销最新的当前有效放行")
		}
		// 存在未结束方案说明标本仍在制备链路中，不能在此刻收回放行。
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_blocks_revocation", "存在未结束方案，不能撤销研究放行")
		}
		if err := record.Revoke(request.Reason, request.RevokedBy, s.now()); err != nil {
			return err
		}
		if err := specimen.RevokeAvailability(s.now()); err != nil {
			return err
		}
		m.PutRelease(record)
		m.PutSpecimen(specimen)
		revoked = record
		return nil
	})
	return revoked, err
}

// ListReleases 返回一份标本的完整放行轨迹，包含已撤销记录，供审计追溯。
func (s *Service) ListReleases(
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
		values = m.ListReleasesForSpecimen(specimenID)
		return nil
	})
	return values, err
}
