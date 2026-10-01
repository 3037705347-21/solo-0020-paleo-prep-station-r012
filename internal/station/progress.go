package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) LogProgress(
	ctx context.Context,
	planID string,
	request contracts.ProgressRequest,
) (domain.ProgressEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProgressEntry{}, err
	}

	var created domain.ProgressEntry
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		admission, ok := m.GetAdmission(planID)
		if !ok {
			return contracts.Conflict("plan_admission_missing", "当前方案没有可用的准入事实")
		}
		if err := admission.BeginAttempt(); err != nil {
			return err
		}
		m.DeferAdmission(admission)
		previous := m.ProgressForPlan(planID)
		entry, err := domain.NewProgressEntry(
			s.nextID("progress"),
			plan,
			admission,
			previous,
			request.Stage,
			request.Percent,
			request.Note,
			request.RecordedBy,
			s.now(),
		)
		if err != nil {
			admission.ConsumeAttempt()
			m.DeferAdmission(admission)
			return err
		}
		if plan.Status == domain.PlanApproved {
			if err := plan.Begin(s.now()); err != nil {
				return err
			}
		}
		if request.Percent == 100 {
			if err := plan.Complete(s.now()); err != nil {
				return err
			}
			if err := specimen.MarkStabilized(s.now()); err != nil {
				return err
			}
		}
		m.AppendProgress(entry)
		admission.CompleteAttempt(entry.Sequence, entry.Percent)
		m.PutAdmission(admission)
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		created = entry
		return nil
	})
	return created, err
}
