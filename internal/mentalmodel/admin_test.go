package mentalmodel

import (
	"errors"
	"memd/internal/domain"
	"testing"
)

// TestCreateMentalModelValidatesAndQueuesBuild checks spec validation and that the first build is a
// manual, full refresh queued with the model.
func TestCreateMentalModelValidatesAndQueuesBuild(t *testing.T) {
	e := newEnv(t)
	adm := e.svc
	bad := []domain.MentalModelSpec{
		{ID: "Bad_ID"},
		{ID: "y", Schedule: domain.RefreshSchedule{Cron: "not a cron"}},
		{ID: "z", Scope: domain.MentalModelScope{Tags: domain.TagFilter{Tags: []string{"a"}}}}, // flat tags: spec.tags
	}
	for _, s := range bad {
		if _, _, err := adm.CreateMentalModel(e.ctx, e.bank, s); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("%+v accepted: %v", s, err)
		}
	}
	m, op, err := adm.CreateMentalModel(e.ctx, e.bank, domain.MentalModelSpec{
		ID: "team", Name: "Team", Tags: []string{" Proj "}, FolderPath: "/people/eng/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Spec.FolderPath != "people/eng" || len(m.Spec.Tags) != 1 || m.Spec.Tags[0] != "proj" ||
		op.Kind != domain.OperationKindRefreshMentalModel || op.SerialKey != "mm:team" {
		t.Fatalf("model %+v op %+v", m.Spec, op)
	}
	job, p, ok := claimRefresh(e)
	if !ok || job.ID != op.ID || !p.Manual || !p.ForceFull || p.MentalModelID != "team" {
		t.Fatalf("build job %+v payload %+v", job, p)
	}
	if _, _, err := adm.CreateMentalModel(e.ctx, e.bank, domain.MentalModelSpec{ID: "team"}); !errors.Is(err,
		domain.ErrAlreadyExists) {
		t.Fatalf("duplicate id: %v", err)
	}
}

// TestAdminGetViewAndStale checks view trimming, stale (computed in the full view only) and NOT_FOUND.
func TestAdminGetViewAndStale(t *testing.T) {
	e := newEnv(t)
	adm := e.svc
	builtModel(e, domain.RefreshStrategySingleShot)
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	full, err := adm.GetMentalModel(e.ctx, e.bank, "status", domain.MentalModelViewUnspecified)
	if err != nil || full.ContentMD == "" || !full.Stale {
		t.Fatalf("get full: stale %v err %v", full.Stale, err)
	}
	basic, _ := adm.GetMentalModel(e.ctx, e.bank, "status", domain.MentalModelViewBasic)
	if basic.ContentMD != "" || basic.Stale {
		t.Fatalf("basic view returned content or computed stale (%v)", basic.Stale)
	}
	_, err = adm.GetMentalModel(e.ctx, e.bank, "nope", domain.MentalModelViewFull)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown model: %v", err)
	}
}

// patchFn returns a SpecPatch applying f.
func patchFn(f func(*domain.MentalModelSpec)) SpecPatch {
	return func(s domain.MentalModelSpec) (domain.MentalModelSpec, error) {
		f(&s)
		return s, nil
	}
}

// TestAdminUpdateResetsWatermarkOnScopeChange checks that a rename keeps the watermark, a source-query
// change resets it (the next refresh runs in full) and the id can't change.
func TestAdminUpdateResetsWatermarkOnScopeChange(t *testing.T) {
	e := newEnv(t)
	adm := e.svc
	built, _ := builtModel(e, domain.RefreshStrategySingleShot)
	m, err := adm.UpdateMentalModel(e.ctx, e.bank, "status", patchFn(func(s *domain.MentalModelSpec) {
		s.Name = "Renamed"
	}))
	if err != nil || m.Spec.Name != "Renamed" || m.LastSeqSeen != built.LastSeqSeen {
		t.Fatalf("rename: %v seq %d", err, m.LastSeqSeen)
	}
	m, err = adm.UpdateMentalModel(e.ctx, e.bank, "status", patchFn(func(s *domain.MentalModelSpec) {
		s.SourceQuery, s.ID = "who is on the team?", "hijack"
	}))
	if err != nil || m.Spec.ID != "status" || m.LastSeqSeen != 0 {
		t.Fatalf("query change: %v id %s seq %d", err, m.Spec.ID, m.LastSeqSeen)
	}
}

// TestFoldersListAndDelete checks folder-prefix listing and recursive folder delete.
func TestFoldersListAndDelete(t *testing.T) {
	e := newEnv(t)
	adm := e.svc
	for id, folder := range map[string]string{"a": "eng", "b": "eng/backend", "c": "engineering", "d": ""} {
		e.addModel(domain.MentalModelSpec{ID: domain.MentalModelID(id), FolderPath: folder})
	}
	page, err := adm.ListMentalModels(e.ctx, ListRequest{Bank: e.bank, FolderPrefix: "eng/"})
	if err != nil || len(page.Items) != 2 || page.Items[0].Spec.ID != "a" || page.Items[1].Spec.ID != "b" {
		t.Fatalf("list eng: %+v %v", page.Items, err)
	}
	if _, err := adm.DeleteMentalModelFolder(e.ctx, e.bank, " / "); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("empty folder delete: %v", err)
	}
	n, err := adm.DeleteMentalModelFolder(e.ctx, e.bank, "eng")
	if err != nil || n != 2 {
		t.Fatalf("deleted %d: %v", n, err)
	}
	page, _ = adm.ListMentalModels(e.ctx, ListRequest{Bank: e.bank})
	if len(page.Items) != 2 {
		t.Fatalf("left %+v", page.Items)
	}
	if err := adm.DeleteMentalModel(e.ctx, e.bank, "c"); err != nil {
		t.Fatal(err)
	}
	if err := adm.DeleteMentalModel(e.ctx, e.bank, "c"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// TestScopeChangedComparesSets checks that scopeChanged treats tags and fact types as sets and tag groups
// by value, with nil equal to empty, and still sees real changes.
func TestScopeChangedComparesSets(t *testing.T) {
	leaf := domain.TagGroup{Kind: domain.TagGroupLeaf, Leaf: &domain.TagLeaf{Tags: []string{"a"}}}
	base := domain.MentalModelSpec{SourceQuery: "q", Scope: domain.MentalModelScope{
		FactTypes: []domain.FactType{domain.FactTypeWorld, domain.FactTypeExperience},
		Tags:      domain.TagFilter{Groups: []domain.TagGroup{leaf}},
	}}
	same := base
	same.Tags = []string{}
	same.Scope.FactTypes = []domain.FactType{domain.FactTypeExperience, domain.FactTypeWorld}
	same.Scope.Tags.Groups = []domain.TagGroup{leaf}
	empty, emptied := domain.MentalModelSpec{}, domain.MentalModelSpec{Tags: []string{}}
	emptied.Scope.FactTypes, emptied.Scope.Tags.Groups = []domain.FactType{}, []domain.TagGroup{}
	if scopeChanged(base, same) || scopeChanged(empty, emptied) {
		t.Fatal("equal scopes reported changed")
	}
	other := same
	other.Scope.FactTypes = []domain.FactType{domain.FactTypeWorld}
	if !scopeChanged(base, other) || !scopeChanged(empty, base) {
		t.Fatal("changed scope not reported")
	}
}
