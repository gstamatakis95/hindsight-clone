package postgres_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOrgs: an org must exist before a bank is created in it; Create, Get, SetConfig and ListOrgs behave as
// documented; a tombstoned org refuses bank creation and leaves ListOrgs.
func TestOrgs(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	err := ensureOnly(t, s, domain.NewBankRef("t", "missing", "b"))
	wantErrIs(t, "Ensure(unknown org)", err, domain.ErrOrgNotFound)
	err = txErr(t, s, func(tx store.Tx) error {
		return tx.Banks().Create(ctx, domain.Bank{Ref: domain.NewBankRef("t", "missing", "b")})
	})
	wantErrIs(t, "Create(bank in unknown org)", err, domain.ErrOrgNotFound)
	err = txErr(t, s, func(tx store.Tx) error {
		return tx.Banks().SetConfig(ctx, domain.NewBankRef("t", "missing", "b"), domain.BankConfig{})
	})
	wantErrIs(t, "SetConfig(bank in unknown org)", err, domain.ErrOrgNotFound)

	cfg := domain.BankConfig{Retain: &domain.RetainConfig{Mission: "org mission"}}
	for _, id := range []string{"b-org", "a-org", "a-other"} {
		inTx(t, s, func(tx store.Tx) error {
			return tx.Orgs().Create(ctx, domain.Org{Ref: domain.OrgRef{Tenant: "t", Org: id}, Name: id, Config: cfg})
		})
	}
	err = txErr(t, s, func(tx store.Tx) error {
		return tx.Orgs().Create(ctx, domain.Org{Ref: domain.OrgRef{Tenant: "t", Org: "a-org"}})
	})
	wantErrIs(t, "Create(duplicate org)", err, domain.ErrAlreadyExists)
	var got domain.Org
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Orgs().SetConfig(ctx, domain.OrgRef{Tenant: "t", Org: "a-org"}, domain.BankConfig{}); err != nil {
			return err
		}
		got, err = tx.Orgs().Get(ctx, domain.OrgRef{Tenant: "t", Org: "b-org"})
		return err
	})
	if got.Name != "b-org" || got.Config.Retain == nil || got.Config.Retain.Mission != "org mission" {
		t.Errorf("Get(b-org) = %+v, want its name and config", got)
	}
	if err := ensureOnly(t, s, domain.NewBankRef("t", "a-org", "b")); err != nil {
		t.Fatalf("Ensure in an existing org: %v", err)
	}
	markOrgDeleting(t, pgtest.Pool(t), domain.OrgRef{Tenant: "t", Org: "b-org"})
	err = ensureOnly(t, s, domain.NewBankRef("t", "b-org", "b"))
	wantErrIs(t, "Ensure(tombstoned org)", err, domain.ErrOrgDeleting)
	err = txErr(t, s, func(tx store.Tx) error {
		return tx.Orgs().Create(ctx, domain.Org{Ref: domain.OrgRef{Tenant: "t", Org: "b-org"}})
	})
	wantErrIs(t, "Create(tombstoned org)", err, domain.ErrOrgDeleting)
	orgs, err := s.Read().ListOrgs(ctx, store.OrgQuery{Tenant: "t", IDPrefix: "a-", Limit: 10})
	if ids := orgIDs(orgs); err != nil || !slices.Equal(ids, []string{"a-org", "a-other"}) {
		t.Errorf("ListOrgs(a-) = %v, %v; want [a-org a-other]", ids, err)
	}
	orgs, err = s.Read().ListOrgs(ctx, store.OrgQuery{Tenant: "t", After: "a-org", Limit: 10})
	if ids := orgIDs(orgs); err != nil || !slices.Equal(ids, []string{"a-other"}) {
		t.Errorf("ListOrgs(after a-org) = %v, %v; want [a-other] (the tombstoned b-org left out)", ids, err)
	}
}

// ensureOnly runs Ensure in its own transaction without creating the org first.
func ensureOnly(t *testing.T, s store.DB, b domain.BankRef) error {
	t.Helper()
	return s.InTx(t.Context(), func(tx store.Tx) error {
		_, err := tx.Banks().Ensure(t.Context(), b)
		return err
	})
}

// orgIDs returns the ids of orgs.
func orgIDs(orgs []domain.Org) []string {
	out := make([]string, len(orgs))
	for i, o := range orgs {
		out[i] = o.Ref.Org
	}
	return out
}

// markOrgDeleting tombstones the org with SQL, as an org delete does.
func markOrgDeleting(t *testing.T, pool *pgxpool.Pool, o domain.OrgRef) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE orgs SET deleting_at = now() WHERE tenant_id = $1 AND id = $2`,
		o.Tenant, o.Org); err != nil {
		t.Fatalf("tombstone org %s: %v", o, err)
	}
}

// TestEnsureWaitsForOrgDelete: an org delete locks the org row FOR UPDATE before it tombstones the org's banks.
// A concurrent Ensure share-locks the same row, so it waits for that transaction and then sees the tombstone
// (ErrOrgDeleting) instead of creating a bank the delete would miss.
func TestEnsureWaitsForOrgDelete(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	b := domain.NewBankRef("t", "o", "late")
	pgtest.CreateOrgs(t, s, b)
	ctx := t.Context()
	del, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = del.Rollback(ctx) }()
	if _, err := del.Exec(ctx, `SELECT 1 FROM orgs WHERE tenant_id = 't' AND id = 'o' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- ensureOnly(t, s, b)
	}()
	select {
	case err := <-done:
		t.Fatalf("Ensure returned %v while the org row was locked FOR UPDATE; want it to wait", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := del.Exec(ctx, `UPDATE orgs SET deleting_at = now() WHERE tenant_id = 't' AND id = 'o'`); err != nil {
		t.Fatal(err)
	}
	if err := del.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		wantErrIs(t, "Ensure after the org delete committed", err, domain.ErrOrgDeleting)
	case <-time.After(waitTimeout):
		t.Fatal("Ensure still waiting after the org delete committed")
	}
}

// TestOrgConfigUpdateDoesNotBlockBanks: an org config update (a plain UPDATE, FOR NO KEY UPDATE) does not conflict
// with bank creation's FOR KEY SHARE, so Ensure in the org proceeds while the update is open.
func TestOrgConfigUpdateDoesNotBlockBanks(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	b := domain.NewBankRef("t", "o", "b")
	pgtest.CreateOrgs(t, s, b)
	ctx := t.Context()
	release, held := make(chan struct{}), make(chan struct{})
	updated := make(chan error, 1)
	go func() {
		updated <- s.InTx(ctx, func(tx store.Tx) error {
			if err := tx.Orgs().LockConfig(ctx, b.OrgRef()); err != nil {
				return err
			}
			if err := tx.Orgs().SetConfig(ctx, b.OrgRef(), domain.BankConfig{}); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	if err := ensureOnly(t, s, b); err != nil {
		t.Errorf("Ensure while an org config update is open: %v", err)
	}
	close(release)
	if err := <-updated; err != nil {
		t.Errorf("org config update: %v", err)
	}
}

// TestRejectEmptyOrg: a BankRef without an org is a programming error the store refuses (ErrInvalidArgument)
// instead of reading or writing nothing.
func TestRejectEmptyOrg(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	noOrg := domain.NewBankRef("t", "", "b")
	for name, fn := range map[string]func(tx store.Tx) error{
		"Ensure": func(tx store.Tx) error { _, err := tx.Banks().Ensure(ctx, noOrg); return err },
		"Get":    func(tx store.Tx) error { _, err := tx.Banks().Get(ctx, noOrg); return err },
		"Seq":    func(tx store.Tx) error { _, err := tx.Seq(ctx, noOrg); return err },
		"Enqueue": func(tx store.Tx) error {
			_, _, err := tx.Queue().Enqueue(ctx, store.Job{Bank: noOrg, Kind: domain.OperationKindConsolidate})
			return err
		},
		"ListUnits": func(tx store.Tx) error { _, err := tx.ListUnits(ctx, store.UnitQuery{Bank: noOrg}); return err },
	} {
		wantErrIs(t, name+"(no org)", txErr(t, s, fn), domain.ErrInvalidArgument)
	}
}

// TestSchemaOrgColumns: every table but tenants and orgs has an org_id column, so a table added later can't
// escape the org boundary (org-wide reads, deletes and checks key on it).
func TestSchemaOrgColumns(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	rows, err := pool.Query(t.Context(), `
		SELECT t.table_name FROM information_schema.tables t
		WHERE t.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		  AND t.table_name NOT IN ('tenants', 'orgs', 'alembic_version')
		  AND NOT EXISTS (SELECT 1 FROM information_schema.columns c
		                  WHERE c.table_schema = 'public' AND c.table_name = t.table_name AND c.column_name = 'org_id')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Errorf("table %s has no org_id column", name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

var (
	// bankScoped matches SQL that names a bank: a bank_id column or the banks table.
	bankScoped = regexp.MustCompile(`\bbank_id\b|\bbanks\b`)
	// sqlish matches a Go string literal that is (part of) a statement rather than a message.
	sqlish = regexp.MustCompile(`\b(SELECT|INSERT|UPDATE|DELETE|FROM|JOIN|WHERE|AND)\b|_id\b`)
)

// TestSQLNamesOrg: every SQL statement that names a bank (a bank_id column or the banks table) also names org_id, so
// a statement can't match the same bank id in another org. It scans the sqlc queries (one statement per
// "-- name:" block) and, for the dynamic builders, the string literals of each function in the package's Go files.
func TestSQLNamesOrg(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("queries/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no queries: %v", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range strings.Split(string(src), "-- name: ")[1:] {
			name, body, _ := strings.Cut(stmt, "\n")
			checkOrgNamed(t, f+": "+strings.Fields(name)[0], stripSQLComments(body))
		}
	}
	gos, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range gos {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for fn, sql := range functionLiterals(t, f) {
			checkOrgNamed(t, f+": "+fn, sql)
		}
	}
}

// checkOrgNamed fails when sql names a bank but not org_id.
func checkOrgNamed(t *testing.T, where, sql string) {
	t.Helper()
	if bankScoped.MatchString(sql) && !strings.Contains(sql, "org_id") {
		t.Errorf("%s names a bank without org_id", where)
	}
}

// stripSQLComments drops "--" comment lines.
func stripSQLComments(sql string) string {
	var b strings.Builder
	for line := range strings.Lines(sql) {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			b.WriteString(line)
		}
	}
	return b.String()
}

// functionLiterals returns, per top-level function of the Go file, its SQL-looking string literals (sqlish) joined
// by spaces; error and log messages are left out.
func functionLiterals(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		var parts []string
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && sqlish.MatchString(v) {
					parts = append(parts, v)
				}
			}
			return true
		})
		out[fd.Name.Name] = strings.Join(parts, " ")
	}
	return out
}
