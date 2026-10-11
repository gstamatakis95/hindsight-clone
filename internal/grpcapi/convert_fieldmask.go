package grpcapi

import (
	"memd/internal/config"
	"memd/internal/configsvc"
	"memd/internal/domain"
	"memd/internal/mentalmodel"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// FieldMask patches. Paths are proto field names, validated against the proto message; config.ApplyPaths
// applies them to the domain struct, whose JSON tags are the proto field names, so a stored value that has
// no proto twin (a non-nil empty list = an explicit clear) survives patches of other fields. A masked path
// replaces the target with the request's value; an unset value clears it, and a masked list or map with no
// elements becomes a non-nil empty one (an explicit clear). An absent or empty mask means every populated
// leaf of the request: set singular messages are recursed into (a set but empty one yields no path), and
// lists, maps, Structs and model bindings are leaves, so a sibling the request leaves unset is never
// touched. A model binding is always set whole: no path goes inside one.

// leafMessages are the singular messages an implicit mask treats as one leaf.
var leafMessages = map[protoreflect.FullName]bool{
	"google.protobuf.Struct": true,
	"memory.v1.ModelBinding": true,
}

// maskPaths validates mask against msg's type and returns its normalized paths; an empty mask yields
// msg's populated leaf paths. Paths in immutable are rejected.
func maskPaths(msg proto.Message, mask *fieldmaskpb.FieldMask, immutable ...string) ([]string, error) {
	if len(mask.GetPaths()) == 0 {
		var paths []string
		populatedLeaves(msg.ProtoReflect(), "", &paths)
		return dropImmutable(paths, immutable), nil
	}
	m := &fieldmaskpb.FieldMask{Paths: append([]string(nil), mask.GetPaths()...)}
	if !m.IsValid(msg) {
		return nil, domain.InvalidField("update_mask", "invalid update_mask %v for %s", mask.GetPaths(),
			msg.ProtoReflect().Descriptor().FullName())
	}
	m.Normalize()
	for _, p := range m.GetPaths() {
		for _, im := range immutable {
			if p == im || strings.HasPrefix(p, im+".") {
				return nil, domain.InvalidField("update_mask", "%s cannot be updated", p)
			}
		}
	}
	return m.GetPaths(), nil
}

// populatedLeaves appends the paths of m's populated leaves: it recurses into set singular messages and
// stops at lists, maps, leafMessages and scalars.
func populatedLeaves(m protoreflect.Message, prefix string, out *[]string) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		path := prefix + string(fd.Name())
		if fd.Message() != nil && !fd.IsList() && !fd.IsMap() && !leafMessages[fd.Message().FullName()] {
			populatedLeaves(v.Message(), path+".", out)
		} else {
			*out = append(*out, path)
		}
		return true
	})
}

// dropImmutable removes immutable top-level paths from an implicit (populated-fields) mask.
func dropImmutable(paths, immutable []string) []string {
	out := paths[:0]
	for _, p := range paths {
		keep := true
		for _, im := range immutable {
			keep = keep && p != im
		}
		if keep {
			out = append(out, p)
		}
	}
	return out
}

// configPatch builds the ConfigPatch of an Update*Config request: the masked paths applied to the stored
// level, which must then pass config.Validate.
func configPatch(patch *pb.BankConfig, mask *fieldmaskpb.FieldMask) (configsvc.Patch, error) {
	if patch == nil {
		patch = &pb.BankConfig{}
	}
	paths, err := maskPaths(patch, mask)
	if err != nil {
		return nil, err
	}
	var d decoder
	src := d.config(patch)
	if d.err != nil {
		return nil, d.err
	}
	return func(cur domain.BankConfig) (domain.BankConfig, error) {
		out := cur
		if err := config.ApplyPaths(&out, &src, paths); err != nil {
			return domain.BankConfig{}, err //nolint:wrapcheck // config errors are already domain error classes
		}
		if err := config.Validate(out); err != nil {
			return domain.BankConfig{}, err //nolint:wrapcheck // config errors are already domain error classes
		}
		return out, nil
	}, nil
}

// resetPaths returns the paths of a Reset*Config reset_mask: the BankConfig paths to unset, or config.ResetAll
// alone when the mask holds "*". An empty mask, and a path that is not a BankConfig path, are
// domain.ErrInvalidArgument on reset_mask.
func resetPaths(mask *fieldmaskpb.FieldMask) ([]string, error) {
	if len(mask.GetPaths()) == 0 {
		return nil, domain.InvalidField("reset_mask", "reset_mask is required (%q resets everything)", config.ResetAll)
	}
	if slices.Contains(mask.GetPaths(), config.ResetAll) {
		return []string{config.ResetAll}, nil
	}
	m := &fieldmaskpb.FieldMask{Paths: append([]string(nil), mask.GetPaths()...)}
	if !m.IsValid(&pb.BankConfig{}) {
		return nil, domain.InvalidField("reset_mask", "invalid reset_mask %v for memory.v1.BankConfig", mask.GetPaths())
	}
	m.Normalize()
	return m.GetPaths(), nil
}

// directiveFields are the Directive paths UpdateDirective may name.
var directiveFields = []string{"name", "rules", "tags", "active"}

// directivePatch builds the DirectivePatch of UpdateDirective: each masked path (name, rules, tags, active) is
// copied from the request's directive, so a masked field left unset clears it. The id cannot be changed.
func directivePatch(patch *pb.Directive, mask *fieldmaskpb.FieldMask) (configsvc.DirectivePatch, error) {
	if patch == nil {
		patch = &pb.Directive{}
	}
	paths, err := maskPaths(patch, mask, "id")
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if !slices.Contains(directiveFields, p) {
			return nil, domain.InvalidField("update_mask", "invalid update_mask path %q", p)
		}
	}
	src := directiveFromProto(patch)
	return func(cur domain.Directive) (domain.Directive, error) {
		out := cur
		for _, p := range paths {
			switch p {
			case "name":
				out.Name = src.Name
			case "rules":
				out.Rules = slices.Clone(src.Rules)
			case "tags":
				out.Tags = slices.Clone(src.Tags)
			case "active":
				out.Active = src.Active
			}
		}
		return out, nil
	}, nil
}

// scheduleWhen pairs the members of RefreshSchedule's when oneof: a path to one also applies the other, so
// setting one member clears the other, as on the wire.
var scheduleWhen = map[string]string{
	"schedule.after_consolidation": "schedule.cron",
	"schedule.cron":                "schedule.after_consolidation",
}

// specPatch builds the SpecPatch of UpdateMentalModel; the id cannot be changed. A path to either member of
// schedule.when also clears the other. Paths inside overrides name RefreshOverrides fields and are mapped onto
// the stored config level (refreshPath); the caller validates the patched spec's overrides.
func specPatch(patch *pb.MentalModelSpec, mask *fieldmaskpb.FieldMask) (mentalmodel.SpecPatch, error) {
	if patch == nil {
		patch = &pb.MentalModelSpec{}
	}
	paths, err := maskPaths(patch, mask, "id")
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if other, ok := scheduleWhen[p]; ok && !slices.Contains(paths, other) {
			paths = append(paths, other)
		}
	}
	for i, p := range paths {
		rest, ok := strings.CutPrefix(p, "overrides.")
		if !ok {
			continue
		}
		mapped, ok := refreshPath(rest)
		if !ok {
			return nil, domain.InvalidField("update_mask", "invalid update_mask path %q", p)
		}
		paths[i] = "overrides." + mapped
	}
	var d decoder
	src := d.spec(patch)
	if d.err != nil {
		return nil, d.err
	}
	return func(cur domain.MentalModelSpec) (domain.MentalModelSpec, error) {
		out := cur
		if err := config.ApplyPaths(&out, &src, paths); err != nil {
			return domain.MentalModelSpec{}, err //nolint:wrapcheck // config errors are already domain error classes
		}
		return out, nil
	}, nil
}
