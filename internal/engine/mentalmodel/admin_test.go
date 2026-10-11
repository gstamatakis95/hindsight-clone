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
	_, adm := newTriggers(e)
	bad := []domain.MentalModelSpec{
		{ID: "Bad_ID"},
		{ID: "x", Trigger: domain.RefreshTrigger{RefreshAfterConsolidation: true, RefreshCron: "0 * * * *"}},
		{ID: "y", Trigger: domain.RefreshTrigger{RefreshCron: "not a cron"}},
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

// TestAdminGetDetailAndStale checks detail trimming, is_stale (computed at full detail only) and NOT_FOUND.
func TestAdminGetDetailAndStale(t *testing.T) {
	e := newEnv(t)
	_, adm := newTriggers(e)
	builtModel(e, domain.RefreshStrategySingleShot)
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	full, err := adm.GetMentalModel(e.ctx, e.bank, "status", domain.DetailUnspecified)
	if err != nil || full.ContentMD == "" || !full.IsStale {
		t.Fatalf("get full: stale %v err %v", full.IsStale, err)
	}
	meta, _ := adm.GetMentalModel(e.ctx, e.bank, "status", domain.DetailMetadata)
	if meta.ContentMD != "" || meta.IsStale {
		t.Fatalf("metadata detail returned content or computed is_stale (%v)", meta.IsStale)
	}
	if _, err := adm.GetMentalModel(e.ctx, e.bank, "nope", domain.DetailFull); !errors.Is(err, domain.ErrNotFound) {
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
	_, adm := newTriggers(e)
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

// TestAdminClearKeepsHistory checks that clear empties the model and keeps the content in history.
func TestAdminClearKeepsHistory(t *testing.T) {
	e := newEnv(t)
	_, adm := newTriggers(e)
	built, _ := builtModel(e, domain.RefreshStrategySingleShot)
	cleared, err := adm.ClearMentalModel(e.ctx, e.bank, "status")
	if err != nil || cleared.ContentMD != "" || len(cleared.SourceIDs) != 0 || cleared.LastSeqSeen != 0 {
		t.Fatalf("clear: %v %+v", err, cleared)
	}
	page, err := adm.MentalModelHistory(e.ctx, HistoryRequest{Bank: e.bank, ID: "status", PageSize: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].PreviousContent != built.ContentMD ||
		page.NextPageToken != "" {
		t.Fatalf("history %+v %v", page, err)
	}
}

// TestFoldersListAndDelete checks folder-prefix listing and recursive folder delete.
func TestFoldersListAndDelete(t *testing.T) {
	e := newEnv(t)
	_, adm := newTriggers(e)
	for id, folder := range map[string]string{"a": "eng", "b": "eng/backend", "c": "engineering", "d": ""} {
		e.addModel(domain.MentalModelSpec{ID: domain.MentalModelID(id), FolderPath: folder})
	}
	page, err := adm.ListMentalModels(e.ctx, ListRequest{Bank: e.bank, FolderPrefix: "eng/"})
	if err != nil || len(page.Items) != 2 || page.Items[0].Spec.ID != "a" || page.Items[1].Spec.ID != "b" {
		t.Fatalf("list eng: %+v %v", page.Items, err)
	}
	if _, err := adm.DeleteFolder(e.ctx, e.bank, " / "); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("empty folder delete: %v", err)
	}
	n, err := adm.DeleteFolder(e.ctx, e.bank, "eng")
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
	base := domain.MentalModelSpec{SourceQuery: "q", Trigger: domain.RefreshTrigger{
		FactTypes: []domain.FactType{domain.FactTypeWorld, domain.FactTypeExperience},
		TagGroups: []domain.TagGroup{leaf},
	}}
	same := base
	same.Tags = []string{}
	same.Trigger.FactTypes = []domain.FactType{domain.FactTypeExperience, domain.FactTypeWorld}
	same.Trigger.TagGroups = []domain.TagGroup{leaf}
	empty, emptied := domain.MentalModelSpec{}, domain.MentalModelSpec{Tags: []string{}}
	emptied.Trigger.FactTypes, emptied.Trigger.TagGroups = []domain.FactType{}, []domain.TagGroup{}
	if scopeChanged(base, same) || scopeChanged(empty, emptied) {
		t.Fatal("equal scopes reported changed")
	}
	other := same
	other.Trigger.FactTypes = []domain.FactType{domain.FactTypeWorld}
	if !scopeChanged(base, other) || !scopeChanged(empty, base) {
		t.Fatal("changed scope not reported")
	}
}
