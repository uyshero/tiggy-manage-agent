package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

const evaluationExperimentReconcileLimit = 5

type evaluationExperimentRunPair struct {
	LeftSession  managedagents.Session
	LeftTurnID   string
	LeftEvents   []managedagents.Event
	RightSession managedagents.Session
	RightTurnID  string
	RightEvents  []managedagents.Event
}

func (s *Server) evaluationExperimentStore(w http.ResponseWriter) (managedagents.EvaluationExperimentStore, bool) {
	store, ok := s.store.(managedagents.EvaluationExperimentStore)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "evaluation experiment store is unavailable"})
	}
	return store, ok
}

func (s *Server) createEvaluationDataset(w http.ResponseWriter, r *http.Request) {
	store, ok := s.evaluationExperimentStore(w)
	if !ok {
		return
	}
	var input managedagents.CreateEvaluationDatasetInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	if input.WorkspaceID == "" {
		input.WorkspaceID = managedagents.DefaultWorkspaceID
	}
	input.CreatedBy = requestActorID(r, input.CreatedBy)
	dataset, err := store.CreateEvaluationDatasetContext(r.Context(), input)
	s.recordEvaluationAudit(r, "evaluation.dataset.create", "evaluation_dataset", dataset.ID, input.WorkspaceID, "", err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, dataset)
}

func (s *Server) listEvaluationDatasets(w http.ResponseWriter, r *http.Request) {
	store, ok := s.evaluationExperimentStore(w)
	if !ok {
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	datasets, err := store.ListEvaluationDatasetsContext(r.Context(), workspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasets": nonNilSlice(datasets)})
}

func (s *Server) getEvaluationDataset(w http.ResponseWriter, r *http.Request) {
	store, ok := s.evaluationExperimentStore(w)
	if !ok {
		return
	}
	dataset, err := store.GetEvaluationDatasetContext(r.Context(), r.PathValue("dataset_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dataset)
}

func (s *Server) createEvaluationExperiment(w http.ResponseWriter, r *http.Request) {
	store, ok := s.evaluationExperimentStore(w)
	if !ok {
		return
	}
	var input managedagents.CreateEvaluationExperimentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	leftTemplate, err := s.getSessionForRequest(r, strings.TrimSpace(input.LeftTemplateSessionID))
	if err != nil {
		writeError(w, err)
		return
	}
	rightTemplate, err := s.getSessionForRequest(r, strings.TrimSpace(input.RightTemplateSessionID))
	if err != nil {
		writeError(w, err)
		return
	}
	if leftTemplate.WorkspaceID != rightTemplate.WorkspaceID {
		writeError(w, fmt.Errorf("%w: experiment templates must belong to the same workspace", managedagents.ErrInvalid))
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, leftTemplate.WorkspaceID)
	input.CreatedBy = requestActorID(r, input.CreatedBy)
	experiment, err := store.CreateEvaluationExperimentContext(r.Context(), input)
	if err != nil {
		s.recordEvaluationAudit(r, "evaluation.experiment.create", "evaluation_experiment", "", input.WorkspaceID, leftTemplate.ID, err)
		writeError(w, err)
		return
	}

	for _, item := range experiment.Items {
		pair, pairErr := s.createEvaluationExperimentRunPair(r, experiment, item, leftTemplate, rightTemplate)
		if pairErr != nil {
			experiment, _ = store.UpdateEvaluationExperimentItemContext(r.Context(), managedagents.UpdateEvaluationExperimentItemInput{
				ExperimentID: experiment.ID, ItemID: item.ID,
				Status: managedagents.EvaluationExperimentItemStatusFailed, ErrorMessage: pairErr.Error(),
			})
			continue
		}
		experiment, err = store.UpdateEvaluationExperimentItemContext(r.Context(), managedagents.UpdateEvaluationExperimentItemInput{
			ExperimentID: experiment.ID, ItemID: item.ID,
			LeftSessionID: pair.LeftSession.ID, LeftTurnID: pair.LeftTurnID,
			RightSessionID: pair.RightSession.ID, RightTurnID: pair.RightTurnID,
			Status: managedagents.EvaluationExperimentItemStatusRunning,
		})
		if err != nil {
			break
		}
		s.dispatchRunnerEvents(r, pair.LeftSession.ID, pair.LeftEvents)
		s.dispatchRunnerEvents(r, pair.RightSession.ID, pair.RightEvents)
	}
	s.recordEvaluationAudit(r, "evaluation.experiment.create", "evaluation_experiment", experiment.ID, experiment.WorkspaceID, leftTemplate.ID, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, experiment)
}

func (s *Server) createEvaluationExperimentRunPair(r *http.Request, experiment managedagents.EvaluationExperiment, item managedagents.EvaluationExperimentItem, leftTemplate managedagents.Session, rightTemplate managedagents.Session) (evaluationExperimentRunPair, error) {
	created := []managedagents.Session{}
	cleanup := func() {
		for _, session := range created {
			_ = managedagents.DeleteSessionWithContext(r.Context(), s.store, session.ID)
		}
	}
	createFromTemplate := func(template managedagents.Session, side string) (managedagents.Session, error) {
		session, err := managedagents.CreateSessionWithContext(r.Context(), s.store, managedagents.CreateSessionInput{
			WorkspaceID: template.WorkspaceID, OwnerID: requestOwnerID(r, template.OwnerID),
			AgentID: template.AgentID, AgentConfigVersion: template.AgentConfigVersion,
			EnvironmentID: template.EnvironmentID,
			Title:         fmt.Sprintf("%s · %s · #%d", experiment.Name, side, item.ItemIndex+1),
			CreatedBy:     requestActorID(r, template.CreatedBy),
		})
		if err != nil {
			return managedagents.Session{}, err
		}
		created = append(created, session)
		session, err = managedagents.UpdateSessionRuntimeSettingsWithContext(r.Context(), s.store, session.ID, managedagents.UpdateSessionRuntimeSettingsInput{
			RuntimeSettings: cloneRuntimeSettings(template.RuntimeSettings), ExpectedRevision: session.RuntimeSettingsRevision,
		})
		return session, err
	}
	left, err := createFromTemplate(leftTemplate, "A")
	if err != nil {
		cleanup()
		return evaluationExperimentRunPair{}, err
	}
	right, err := createFromTemplate(rightTemplate, "B")
	if err != nil {
		cleanup()
		return evaluationExperimentRunPair{}, err
	}
	payload, err := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": item.Prompt}}})
	if err != nil {
		cleanup()
		return evaluationExperimentRunPair{}, err
	}
	leftEvents, err := managedagents.AppendEventsWithContext(r.Context(), s.store, left.ID, []managedagents.AppendEventInput{{Type: managedagents.EventUserMessage, Payload: payload}})
	if err != nil {
		cleanup()
		return evaluationExperimentRunPair{}, err
	}
	rightEvents, err := managedagents.AppendEventsWithContext(r.Context(), s.store, right.ID, []managedagents.AppendEventInput{{Type: managedagents.EventUserMessage, Payload: payload}})
	if err != nil {
		cleanup()
		return evaluationExperimentRunPair{}, err
	}
	leftTurnID := experimentTurnID(leftEvents)
	rightTurnID := experimentTurnID(rightEvents)
	if leftTurnID == "" || rightTurnID == "" {
		cleanup()
		return evaluationExperimentRunPair{}, fmt.Errorf("%w: experiment Run identities were not created", managedagents.ErrInvalid)
	}
	return evaluationExperimentRunPair{
		LeftSession: left, LeftTurnID: leftTurnID, LeftEvents: leftEvents,
		RightSession: right, RightTurnID: rightTurnID, RightEvents: rightEvents,
	}, nil
}

func experimentTurnID(events []managedagents.Event) string {
	for _, event := range events {
		if event.Type == managedagents.EventUserMessage {
			return payloadString(event.Payload, "turn_id")
		}
	}
	return ""
}

func (s *Server) listEvaluationExperiments(w http.ResponseWriter, r *http.Request) {
	store, ok := s.evaluationExperimentStore(w)
	if !ok {
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	limit, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if err != nil && strings.TrimSpace(r.URL.Query().Get("limit")) != "" {
		writeError(w, fmt.Errorf("%w: invalid experiment limit", managedagents.ErrInvalid))
		return
	}
	experiments, err := store.ListEvaluationExperimentsContext(r.Context(), workspaceID, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"experiments": nonNilSlice(experiments)})
}

func (s *Server) getEvaluationExperiment(w http.ResponseWriter, r *http.Request) {
	store, ok := s.evaluationExperimentStore(w)
	if !ok {
		return
	}
	experiment, err := store.GetEvaluationExperimentContext(r.Context(), r.PathValue("experiment_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, experiment)
}

func (s *Server) reconcileEvaluationExperiment(w http.ResponseWriter, r *http.Request) {
	store, ok := s.evaluationExperimentStore(w)
	if !ok {
		return
	}
	evaluationStore, ok := s.evaluationStore(w)
	if !ok {
		return
	}
	experiment, err := store.GetEvaluationExperimentContext(r.Context(), r.PathValue("experiment_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	runStore, ok := s.store.(managedagents.SessionRunStore)
	if !ok {
		writeError(w, fmt.Errorf("%w: run store unavailable", managedagents.ErrInvalid))
		return
	}
	processed := 0
	for _, item := range experiment.Items {
		if processed >= evaluationExperimentReconcileLimit || item.EvaluationID != "" || item.LeftSessionID == "" || item.LeftTurnID == "" || item.RightSessionID == "" || item.RightTurnID == "" {
			continue
		}
		leftRun, leftErr := runStore.GetSessionRunContext(r.Context(), item.LeftSessionID, item.LeftTurnID)
		rightRun, rightErr := runStore.GetSessionRunContext(r.Context(), item.RightSessionID, item.RightTurnID)
		if leftErr != nil || rightErr != nil || !evaluationRunTerminal(leftRun.Status) || !evaluationRunTerminal(rightRun.Status) {
			continue
		}
		processed++
		evaluationID := ""
		left, judgeErr := s.runComparisonSnapshot(r, item.LeftSessionID, item.LeftTurnID)
		if judgeErr == nil {
			var right sessionComparisonSide
			right, judgeErr = s.runComparisonSnapshot(r, item.RightSessionID, item.RightTurnID)
			if judgeErr == nil {
				var result autoJudgeResult
				result, judgeErr = s.generateAutoJudgeResultWithReference(r.Context(), experiment.RubricID, item.ExpectedOutput, left, right)
				if judgeErr == nil {
					var evaluation managedagents.RunEvaluation
					evaluation, judgeErr = evaluationStore.CreateRunEvaluationContext(r.Context(), managedagents.CreateRunEvaluationInput{
						LeftSessionID: item.LeftSessionID, LeftTurnID: item.LeftTurnID,
						RightSessionID: item.RightSessionID, RightTurnID: item.RightTurnID,
						RubricID: experiment.RubricID, Scores: result.Scores, Conclusion: result.Conclusion,
						Notes: result.Reasoning, CreatedBy: requestActorID(r, ""),
						EvaluationType: managedagents.EvaluationTypeAuto,
						JudgeProvider:  s.defaultLLMProvider, JudgeModel: s.defaultLLMModel, JudgeReasoning: result.Reasoning,
					})
					if judgeErr == nil {
						evaluationID = evaluation.ID
						leftAverage, rightAverage := evaluationScoreAverages(evaluation.Scores)
						experiment, judgeErr = store.UpdateEvaluationExperimentItemContext(r.Context(), managedagents.UpdateEvaluationExperimentItemInput{
							ExperimentID: experiment.ID, ItemID: item.ID, EvaluationID: evaluation.ID,
							Status:     managedagents.EvaluationExperimentItemStatusCompleted,
							Conclusion: evaluation.Conclusion, LeftAverage: leftAverage, RightAverage: rightAverage,
						})
					}
				}
			}
		}
		if judgeErr != nil {
			experiment, _ = store.UpdateEvaluationExperimentItemContext(r.Context(), managedagents.UpdateEvaluationExperimentItemInput{
				ExperimentID: experiment.ID, ItemID: item.ID,
				Status: managedagents.EvaluationExperimentItemStatusFailed, ErrorMessage: judgeErr.Error(),
			})
		}
		s.recordEvaluationAudit(r, "evaluation.run.auto", "run_evaluation", evaluationID, experiment.WorkspaceID, item.LeftSessionID, judgeErr)
	}
	experiment, err = store.GetEvaluationExperimentContext(r.Context(), experiment.ID)
	s.recordEvaluationAudit(r, "evaluation.experiment.reconcile", "evaluation_experiment", experiment.ID, experiment.WorkspaceID, experiment.LeftTemplateSessionID, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, experiment)
}

func evaluationRunTerminal(status string) bool {
	switch status {
	case managedagents.TurnStatusCompleted, managedagents.TurnStatusFailed, managedagents.TurnStatusInterrupted:
		return true
	default:
		return false
	}
}

func evaluationScoreAverages(scores []managedagents.EvaluationCriterionScore) (float64, float64) {
	if len(scores) == 0 {
		return 0, 0
	}
	var left, right float64
	for _, score := range scores {
		left += float64(score.LeftScore)
		right += float64(score.RightScore)
	}
	return left / float64(len(scores)), right / float64(len(scores))
}
