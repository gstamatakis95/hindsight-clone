package grpcapi

import (
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/mentalmodel"
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
		return nil, fmt.Errorf("%w: invalid update_mask %v for %s", domain.ErrInvalidArgument, mask.GetPaths(),
			msg.ProtoReflect().Descriptor().FullName())
	}
	m.Normalize()
	for _, p := range m.GetPaths() {
		for _, im := range immutable {
			if p == im || strings.HasPrefix(p, im+".") {
				return nil, fmt.Errorf("%w: %s cannot be updated", domain.ErrInvalidArgument, p)
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
func configPatch(patch *pb.BankConfig, mask *fieldmaskpb.FieldMask) (bank.ConfigPatch, error) {
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

// resetPatch builds the ConfigPatch of ResetBankConfig: the paths go back to unset (inherit); "*" resets
// everything and no paths at all is domain.ErrInvalidArgument (config.ResetPaths). The paths are checked
// before the patch runs.
func resetPatch(paths []string) (bank.ConfigPatch, error) {
	if _, err := config.ResetPaths(domain.BankConfig{}, paths); err != nil {
		return nil, err //nolint:wrapcheck // config errors are already domain error classes
	}
	return func(cur domain.BankConfig) (domain.BankConfig, error) {
		return config.ResetPaths(cur, paths) //nolint:wrapcheck // config errors are already domain error classes
	}, nil
}

// specPatch builds the SpecPatch of UpdateMentalModel; the id cannot be changed. Paths inside overrides
// name RefreshOverrides fields and are mapped onto the stored config level (refreshPath); the caller
// validates the patched spec's overrides.
func specPatch(patch *pb.MentalModelSpec, mask *fieldmaskpb.FieldMask) (mentalmodel.SpecPatch, error) {
	if patch == nil {
		patch = &pb.MentalModelSpec{}
	}
	paths, err := maskPaths(patch, mask, "id")
	if err != nil {
		return nil, err
	}
	for i, p := range paths {
		rest, ok := strings.CutPrefix(p, "overrides.")
		if !ok {
			continue
		}
		mapped, ok := refreshPath(rest)
		if !ok {
			return nil, fmt.Errorf("%w: invalid update_mask path %q", domain.ErrInvalidArgument, p)
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
