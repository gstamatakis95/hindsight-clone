package org

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// Deps are the Service's dependencies; every field is required.
type Deps struct {
	// DB reads and writes orgs in short transactions.
	DB store.DB
}

// Service serves OrgService: creating, reading and listing a tenant's orgs.
type Service struct {
	d Deps
}

// New returns the Service over d.
func New(d Deps) *Service {
	return &Service{d: d}
}

// CreateOrg creates org (its Ref, Name and Config) and returns it as stored. An existing org is
// domain.ErrAlreadyExists and one being deleted domain.ErrOrgDeleting. The caller validates Config as a whole
// level, as CreateBank does for a bank's.
func (s *Service) CreateOrg(ctx context.Context, org domain.Org) (domain.Org, error) {
	var out domain.Org
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = domain.Org{}
		if err := tx.Orgs().Create(ctx, domain.Org{Ref: org.Ref, Name: org.Name, Config: org.Config}); err != nil {
			return err
		}
		var err error
		out, err = tx.Orgs().Get(ctx, org.Ref)
		return err
	})
	if err != nil {
		return domain.Org{}, fmt.Errorf("create org: %w", err)
	}
	return out, nil
}

// GetOrg returns a live org; an unknown one, or one being deleted, is domain.ErrOrgNotFound.
func (s *Service) GetOrg(ctx context.Context, o domain.OrgRef) (domain.Org, error) {
	var out domain.Org
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		if out, err = tx.Orgs().Get(ctx, o); err != nil {
			return err
		}
		if out.Deleting() {
			return fmt.Errorf("%w %s is being deleted", domain.ErrOrgNotFound, o)
		}
		return nil
	})
	if err != nil {
		return domain.Org{}, err
	}
	return out, nil
}

// ListRequest mirrors memory.v1.ListOrgsRequest.
type ListRequest struct {
	// Tenant is the tenant listed.
	Tenant string
	// IDPrefix keeps orgs whose id starts with it; empty = all.
	IDPrefix string
	// PageSize caps the page (domain.ListPage); 0 = the default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// Page is one page of orgs.
type Page struct {
	// Orgs are the page's orgs, in id order.
	Orgs []domain.Org
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListOrgs returns one keyset page of the tenant's live orgs in byte order of id (orgs being deleted are left out).
// The page size follows domain.ListPage, and the page token carries the last id of the previous page; a malformed
// one is domain.ErrInvalidArgument.
func (s *Service) ListOrgs(ctx context.Context, req ListRequest) (Page, error) {
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return Page{}, fmt.Errorf("list orgs: %w", err)
	}
	var tok orgToken
	if req.PageToken != "" {
		if err := domain.DecodePageToken(req.PageToken, &tok); err != nil {
			return Page{}, fmt.Errorf("list orgs: %w", err)
		}
	}
	orgs, err := s.d.DB.Read().ListOrgs(ctx, store.OrgQuery{
		Tenant: req.Tenant, IDPrefix: req.IDPrefix, After: tok.After, Limit: size + 1,
	})
	if err != nil {
		return Page{}, fmt.Errorf("list orgs: %w", err)
	}
	page := Page{Orgs: orgs}
	if len(orgs) > size {
		page.Orgs = orgs[:size]
		if page.NextPageToken, err = domain.EncodePageToken(orgToken{After: orgs[size-1].Ref.Org}); err != nil {
			return Page{}, fmt.Errorf("list orgs: %w", err)
		}
	}
	return page, nil
}

// orgToken is the decoded form of a ListOrgs page token.
type orgToken struct {
	// After is the last org id of the previous page.
	After string `json:"a"`
}
