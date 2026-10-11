package retain

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"strings"
)

// LabelResolver maps extracted key:value labels to exact-match label entities.
type LabelResolver interface {
	// Resolve keeps the labels that defs declare (a value or multi_values label only with one of its allowed
	// values, matched case-insensitively and stored in the declared spelling; a map label's field as
	// "key:field"), upserts one label entity per (key, value) and returns their ids plus the "key:value"
	// tags of labels declared with write_to_tags, to add to the unit's tags. Undeclared labels are dropped.
	Resolve(
		ctx context.Context, tx store.Tx, b domain.BankRef, defs []domain.EntityLabel, labels []domain.Label,
	) ([]domain.EntityID, []string, error)
}

// labelResolver is the LabelResolver over EntityRepo.
type labelResolver struct{}

var _ LabelResolver = (*labelResolver)(nil)

// NewLabelResolver returns the LabelResolver.
func NewLabelResolver() LabelResolver { return &labelResolver{} }

// Resolve implements LabelResolver.
func (*labelResolver) Resolve(
	ctx context.Context, tx store.Tx, b domain.BankRef, defs []domain.EntityLabel, labels []domain.Label,
) ([]domain.EntityID, []string, error) {
	var ids []domain.EntityID
	var tags []string
	seen := map[string]bool{}
	for _, l := range labels {
		key, value, toTags, ok := matchLabel(defs, l)
		if !ok || seen[key+"\x00"+value] {
			continue
		}
		seen[key+"\x00"+value] = true
		id, err := tx.Entities().Upsert(ctx, domain.Entity{
			Bank: b, CanonicalName: key + ":" + value, Kind: key, LabelValue: value,
		})
		if err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		if toTags {
			tags = append(tags, key+":"+value)
		}
	}
	return ids, tags, nil
}

// matchLabel finds l's declaration and returns the canonical key and value and whether it is written to
// tags; ok is false for an undeclared key, an empty or placeholder value or a value outside a fixed list.
func matchLabel(defs []domain.EntityLabel, l domain.Label) (key, value string, toTags, ok bool) {
	key, value = strings.TrimSpace(l.Key), strings.TrimSpace(l.Value)
	if key == "" || emptyLabelValue(value) {
		return "", "", false, false
	}
	group, field, isField := strings.Cut(key, ":")
	for _, d := range defs {
		if !strings.EqualFold(d.Key, group) {
			continue
		}
		def, parentTags := d, false
		if isField {
			f, found := fieldDef(d, field)
			if !found {
				return "", "", false, false
			}
			def, parentTags = f, d.WriteToTags
			key = d.Key + ":" + fieldName(d, field)
		} else {
			key = d.Key
		}
		canon, valid := allowedValue(def, value)
		return key, canon, def.WriteToTags || parentTags, valid
	}
	return "", "", false, false
}

// emptyLabelValue reports whether a trimmed label value is blank or a placeholder the model writes for "no
// value" (N/A, none, null, case-insensitive).
func emptyLabelValue(v string) bool {
	switch strings.ToLower(v) {
	case "", "n/a", "none", "null":
		return true
	}
	return false
}

// fieldDef returns the map label's field declaration, matched case-insensitively.
func fieldDef(d domain.EntityLabel, field string) (domain.EntityLabel, bool) {
	if d.Type != domain.LabelTypeMap {
		return domain.EntityLabel{}, false
	}
	name := fieldName(d, field)
	f, ok := d.Fields[name]
	return f, ok
}

// fieldName returns the declared spelling of a map label's field (field itself when undeclared).
func fieldName(d domain.EntityLabel, field string) string {
	for name := range d.Fields {
		if strings.EqualFold(name, field) {
			return name
		}
	}
	return field
}

// allowedValue checks value against a value / multi_values label's list and returns its declared spelling;
// text labels (and a value label declaring no list) take any value. A map label itself carries no value.
func allowedValue(def domain.EntityLabel, value string) (string, bool) {
	switch def.Type {
	case domain.LabelTypeText, domain.LabelTypeMultiText:
		return value, true
	case domain.LabelTypeMap:
		return "", false
	}
	if len(def.Values) == 0 {
		return value, true
	}
	for _, v := range def.Values {
		if strings.EqualFold(v.Value, value) {
			return v.Value, true
		}
	}
	return "", false
}
