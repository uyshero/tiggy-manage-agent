package managedagents

import "context"

type SubagentTaskGroupContextStore interface {
	CreateSubagentTaskGroupContext(ctx context.Context, input CreateSubagentTaskGroupInput) (SubagentTaskGroup, error)
	AppendSubagentTaskGroupItemContext(ctx context.Context, groupID string, input AppendSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error)
	UpdateSubagentTaskGroupItemContext(ctx context.Context, groupID string, itemIndex int, input UpdateSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error)
	GetSubagentTaskGroupContext(ctx context.Context, id string) (SubagentTaskGroup, error)
	ListSubagentTaskGroupsByParentSessionContext(ctx context.Context, parentSessionID string) ([]SubagentTaskGroup, error)
	GetSubagentTaskGroupItemBySessionContext(ctx context.Context, sessionID string) (SubagentTaskGroupItem, error)
	ListSubagentTaskGroupItemsContext(ctx context.Context, groupID string) ([]SubagentTaskGroupItem, error)
	ListChildSubagentTaskGroupsContext(ctx context.Context, parentGroupID string, parentItemIndex int) ([]SubagentTaskGroup, error)
	CancelSubagentTaskGroupContext(ctx context.Context, input CancelSubagentTaskGroupInput) (SubagentTaskGroup, error)
	ReactivateSubagentTaskGroupContext(ctx context.Context, input ReactivateSubagentTaskGroupInput) (SubagentTaskGroup, error)
}

func taskGroupContextStore(store Store) (SubagentTaskGroupContextStore, bool) {
	scoped, ok := store.(SubagentTaskGroupContextStore)
	return scoped, ok
}

func CreateSubagentTaskGroupWithContext(ctx context.Context, store Store, input CreateSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.CreateSubagentTaskGroupContext(ctx, input)
	}
	return store.CreateSubagentTaskGroup(input)
}

func AppendSubagentTaskGroupItemWithContext(ctx context.Context, store Store, groupID string, input AppendSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.AppendSubagentTaskGroupItemContext(ctx, groupID, input)
	}
	return store.AppendSubagentTaskGroupItem(groupID, input)
}

func UpdateSubagentTaskGroupItemWithContext(ctx context.Context, store Store, groupID string, itemIndex int, input UpdateSubagentTaskGroupItemInput) (SubagentTaskGroupItem, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.UpdateSubagentTaskGroupItemContext(ctx, groupID, itemIndex, input)
	}
	return store.UpdateSubagentTaskGroupItem(groupID, itemIndex, input)
}

func GetSubagentTaskGroupWithContext(ctx context.Context, store Store, id string) (SubagentTaskGroup, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.GetSubagentTaskGroupContext(ctx, id)
	}
	return store.GetSubagentTaskGroup(id)
}

func ListSubagentTaskGroupsByParentSessionWithContext(ctx context.Context, store Store, parentSessionID string) ([]SubagentTaskGroup, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.ListSubagentTaskGroupsByParentSessionContext(ctx, parentSessionID)
	}
	return store.ListSubagentTaskGroupsByParentSession(parentSessionID)
}

func GetSubagentTaskGroupItemBySessionWithContext(ctx context.Context, store Store, sessionID string) (SubagentTaskGroupItem, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.GetSubagentTaskGroupItemBySessionContext(ctx, sessionID)
	}
	return store.GetSubagentTaskGroupItemBySession(sessionID)
}

func ListSubagentTaskGroupItemsWithContext(ctx context.Context, store Store, groupID string) ([]SubagentTaskGroupItem, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.ListSubagentTaskGroupItemsContext(ctx, groupID)
	}
	return store.ListSubagentTaskGroupItems(groupID)
}

func ListChildSubagentTaskGroupsWithContext(ctx context.Context, store Store, parentGroupID string, parentItemIndex int) ([]SubagentTaskGroup, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.ListChildSubagentTaskGroupsContext(ctx, parentGroupID, parentItemIndex)
	}
	return store.ListChildSubagentTaskGroups(parentGroupID, parentItemIndex)
}

func CancelSubagentTaskGroupWithContext(ctx context.Context, store Store, input CancelSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.CancelSubagentTaskGroupContext(ctx, input)
	}
	return store.CancelSubagentTaskGroup(input)
}

func ReactivateSubagentTaskGroupWithContext(ctx context.Context, store Store, input ReactivateSubagentTaskGroupInput) (SubagentTaskGroup, error) {
	if scoped, ok := taskGroupContextStore(store); ok {
		return scoped.ReactivateSubagentTaskGroupContext(ctx, input)
	}
	return store.ReactivateSubagentTaskGroup(input)
}
