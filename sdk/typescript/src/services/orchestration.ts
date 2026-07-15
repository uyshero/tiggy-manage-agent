import type {
  AgentDeliberationResponse,
  AgentDiscussionStrategyList,
  AgentTaskGroupResponse,
  AgentTaskGroupTemplateList,
  CancelAgentDeliberationRequest,
  CancelTaskGroupRequest,
  InspectorTaskGroupState,
  ReapOrphanSubagentsRequest,
  ReapOrphanSubagentsResult,
  RetryAgentDeliberationParticipantRequest,
  SessionTaskGroupTree,
} from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";
import { sessionPath } from "./sessions.js";

export class OrchestrationService extends ServiceBase {
  taskGroupTemplates(signal?: AbortSignal): Promise<AgentTaskGroupTemplateList> {
    return this.transport.requestJSON("GET", "/v2/agent/task-group-templates", undefined, signal ? { signal } : {});
  }

  discussionStrategies(signal?: AbortSignal): Promise<AgentDiscussionStrategyList> {
    return this.transport.requestJSON("GET", "/v2/agent/discussion-strategies", undefined, signal ? { signal } : {});
  }

  listDeliberations(sessionId: string, signal?: AbortSignal): Promise<AgentDeliberationResponse[]> {
    return this.transport.requestJSON<{ deliberations: AgentDeliberationResponse[] }>("GET", `${sessionPath(sessionId)}/deliberations`, undefined, signal ? { signal } : {}).then((value) => value.deliberations);
  }

  getDeliberation(sessionId: string, deliberationId: string, signal?: AbortSignal): Promise<AgentDeliberationResponse> {
    return this.transport.requestJSON("GET", deliberationPath(sessionId, deliberationId), undefined, signal ? { signal } : {});
  }

  cancelDeliberation(sessionId: string, deliberationId: string, request: CancelAgentDeliberationRequest = {}, signal?: AbortSignal): Promise<AgentDeliberationResponse> {
    return this.transport.requestJSON("POST", `${deliberationPath(sessionId, deliberationId)}/cancel`, request, signal ? { signal } : {});
  }

  retryDeliberationParticipant(sessionId: string, deliberationId: string, participantIndex: number, request: RetryAgentDeliberationParticipantRequest, signal?: AbortSignal): Promise<AgentDeliberationResponse> {
    const path = resourcePath(`${deliberationPath(sessionId, deliberationId)}/participants`, participantIndex) + "/retry";
    return this.transport.requestJSON("POST", path, request, signal ? { signal } : {});
  }

  listTaskGroups(sessionId: string, signal?: AbortSignal): Promise<InspectorTaskGroupState[]> {
    return this.transport.requestJSON<{ task_groups: InspectorTaskGroupState[] }>("GET", `${sessionPath(sessionId)}/task-groups`, undefined, signal ? { signal } : {}).then((value) => value.task_groups);
  }

  taskGroupTree(sessionId: string, signal?: AbortSignal): Promise<SessionTaskGroupTree> {
    return this.transport.requestJSON("GET", `${sessionPath(sessionId)}/task-group-tree`, undefined, signal ? { signal } : {});
  }

  getTaskGroup(sessionId: string, groupId: string, signal?: AbortSignal): Promise<InspectorTaskGroupState> {
    return this.transport.requestJSON("GET", taskGroupPath(sessionId, groupId), undefined, signal ? { signal } : {});
  }

  cancelTaskGroup(sessionId: string, groupId: string, request: CancelTaskGroupRequest = {}, signal?: AbortSignal): Promise<AgentTaskGroupResponse> {
    return this.transport.requestJSON("POST", `${taskGroupPath(sessionId, groupId)}/cancel`, request, signal ? { signal } : {});
  }

  retryTaskGroup(sessionId: string, groupId: string, signal?: AbortSignal): Promise<AgentTaskGroupResponse> {
    return this.transport.requestJSON("POST", `${taskGroupPath(sessionId, groupId)}/retry`, {}, signal ? { signal } : {});
  }

  retryTaskGroupItem(sessionId: string, groupId: string, itemIndex: number, signal?: AbortSignal): Promise<AgentTaskGroupResponse> {
    const path = resourcePath(`${taskGroupPath(sessionId, groupId)}/items`, itemIndex) + "/retry";
    return this.transport.requestJSON("POST", path, {}, signal ? { signal } : {});
  }

  reapOrphans(request: ReapOrphanSubagentsRequest = {}, signal?: AbortSignal): Promise<ReapOrphanSubagentsResult> {
    return this.transport.requestJSON("POST", "/v2/subagents/reap-orphans", request, signal ? { signal } : {});
  }
}

function deliberationPath(sessionId: string, deliberationId: string): string {
  return resourcePath(`${sessionPath(sessionId)}/deliberations`, deliberationId);
}

function taskGroupPath(sessionId: string, groupId: string): string {
  return resourcePath(`${sessionPath(sessionId)}/task-groups`, groupId);
}
