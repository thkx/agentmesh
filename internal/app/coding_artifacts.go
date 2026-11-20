package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/thkx/agentmesh/internal/coding"
	"github.com/thkx/agentmesh/internal/collaboration"
)

type codingArtifactPublisher struct { service *collaboration.Service }

func newCodingArtifactPublisher(service *collaboration.Service) (*codingArtifactPublisher, error) {
	if service == nil { return nil, fmt.Errorf("collaboration service is required for coding artifact publication") }
	return &codingArtifactPublisher{service:service}, nil
}

func (p *codingArtifactPublisher) PublishCodingArtifact(ctx context.Context, task coding.Task, snapshot coding.Snapshot, steps []coding.Step) (coding.ArtifactPublication,error) {
	if len(snapshot.Diff)==0 || len(snapshot.Diff)>collaboration.MaxArtifactBytes {
		return coding.ArtifactPublication{}, fmt.Errorf("coding diff must contain 1..%d bytes for artifact publication", collaboration.MaxArtifactBytes)
	}
	workspaceID := "coding-" + task.ID
	slug := strings.ToLower(strings.ReplaceAll(task.ID,"_","-"))
	_, err := p.service.GetWorkspace(ctx,workspaceID)
	if errors.Is(err, collaboration.ErrNotFound) {
		_, err = p.service.CreateWorkspace(ctx,collaboration.Workspace{
			ID:workspaceID, Name:"Coding task "+task.ID, Slug:"coding-"+slug,
			Description:"Evidence workspace for AgentMesh Coding Task "+task.ID,
			AutonomyMode:"directed", MaxAgentConcurrency:1, MaxDelegationDepth:0,
		})
	}
	if err != nil { return coding.ArtifactPublication{}, fmt.Errorf("prepare coding evidence workspace: %w",err) }

	metadata,_ := json.Marshal(map[string]any{
		"coding_task_id":task.ID,"execution_id":task.ExecutionID,"attempt_id":task.AttemptID,
		"repository_path":task.RepositoryPath,"worktree_path":task.WorkspacePath,
		"branch":task.BranchName,"base_commit":snapshot.CommitHash,
		"repair_attempts":task.RepairAttempts,"diff_sha256":snapshot.ContentSHA256,
	})
	artifact,version,err := p.service.CreateArtifact(ctx,collaboration.CreateArtifactRequest{
		ID:"coding-artifact-"+task.ID, VersionID:"coding-version-"+task.ID,
		WorkspaceID:workspaceID, Kind:"code_patch", Title:task.Title+" — code patch",
		Content:[]byte(snapshot.Diff), MediaType:"text/x-diff", Metadata:metadata,
		SourceExecution:task.ExecutionID, SourceAttempt:task.AttemptID,
	})
	if err != nil { return coding.ArtifactPublication{}, fmt.Errorf("create coding patch artifact: %w",err) }

	addEvidence := func(id,kind,source,statement string) error {
		_, evidenceErr := p.service.AddArtifactEvidence(ctx,artifact.ID,collaboration.AddArtifactEvidenceRequest{
			ID:id, VersionID:version.ID, Kind:kind, SourceRef:source,
			ContentSHA256:version.ContentSHA256, Statement:statement,
			CreatedBy:"agentmesh-coding-runtime", ExecutionID:task.ExecutionID, AttemptID:task.AttemptID,
		})
		return evidenceErr
	}
	if err=addEvidence("coding-evidence-"+task.ID+"-diff","diff","coding-task:"+task.ID+":diff",
		fmt.Sprintf("Final Worktree diff based on %s on branch %s; SHA-256 %s",snapshot.CommitHash,task.BranchName,version.ContentSHA256)); err!=nil {
		return coding.ArtifactPublication{}, fmt.Errorf("record patch evidence: %w",err)
	}
	for _,step := range steps {
		switch {
		case step.Kind=="test.run":
			statement:=fmt.Sprintf("Command: %s; status: %s; exit code: %d; duration: %dms; output: %s",step.Input,step.Status,step.ExitCode,step.DurationMillis,truncateCodingEvidence(step.Output,1800))
			if err=addEvidence(fmt.Sprintf("coding-evidence-%s-test-%d",task.ID,step.Sequence),"test",fmt.Sprintf("coding-task:%s:step:%d",task.ID,step.Sequence),truncateCodingEvidence(statement,3900));err!=nil {
				return coding.ArtifactPublication{},fmt.Errorf("record test evidence: %w",err)
			}
		case step.Kind=="review.inspect":
			statement:=fmt.Sprintf("Independent reviewer verdict: %s; findings: %s",step.Status,limit(step.Output,3000))
			if err=addEvidence(fmt.Sprintf("coding-evidence-%s-review-%d",task.ID,step.Sequence),"tool_log",fmt.Sprintf("coding-task:%s:step:%d",task.ID,step.Sequence),limit(statement,3900));err!=nil {
				return coding.ArtifactPublication{},fmt.Errorf("record review evidence: %w",err)
			}
		}
	}
	verification,err:=p.service.VerifyArtifact(ctx,artifact.ID,version.Version,collaboration.ArtifactVerificationPolicy{
		RequiredEvidenceKinds:[]string{"diff","test","tool_log"}, RequireContentDigest:true,
	})
	if err!=nil { return coding.ArtifactPublication{},fmt.Errorf("verify coding artifact: %w",err) }
	return coding.ArtifactPublication{
		ArtifactID:artifact.ID,VersionID:version.ID,
		VerificationID:verification.ID,VerificationOutcome:verification.Outcome,
	},nil
}

func truncateCodingEvidence(value string, max int) string {
	if len(value) <= max { return value }
	return value[:max] + "...[truncated]"
}
