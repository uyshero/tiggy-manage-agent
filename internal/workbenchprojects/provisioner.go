package workbenchprojects

import "context"

type TemplateFile struct {
	Path    string
	Content string
}

type ProvisionInput struct {
	Name                 string
	RepositoryPath       string
	ExistingRepositoryID string
	DefaultBranch        string
	Files                []TemplateFile
}

type ProvisionResult struct {
	RepositoryID  string
	RepositoryURL string
	DefaultBranch string
}

type Provisioner interface {
	Provision(context.Context, ProvisionInput) (ProvisionResult, error)
}
