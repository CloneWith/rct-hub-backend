package graphql

import (
	"context"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/paginate"
)

type CommandExecutor interface {
	Execute(context.Context, matchcommand.Request) (matchcommand.Result, error)
}

type Resolver struct {
	svc      *service.Services
	commands CommandExecutor
}

func NewResolver(svc *service.Services, commands ...CommandExecutor) *Resolver {
	resolver := &Resolver{svc: svc}
	if len(commands) > 0 {
		resolver.commands = commands[0]
	}
	return resolver
}

func buildPageParams(page, perPage *int) paginate.Params {
	params := paginate.Params{}
	if page != nil {
		params.Page = int64(*page)
	}
	if perPage != nil {
		params.PerPage = int64(*perPage)
	}
	params.Normalize()
	return params
}
