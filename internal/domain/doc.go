// Package domain holds memd's plain data types: banks, configuration, memory units, entities, links,
// observation scopes, mental models, directives, templates, operations, events and the error values
// every layer shares.
//
// Domain imports nothing internal (depguard enforces this). Enums are string-valued so they read well
// in JSON columns and logs; the empty string is always the "unspecified" value and mirrors the proto's
// *_UNSPECIFIED = 0. Optional configuration scalars are pointers (nil = unset), nested messages are
// pointers (nil = unset) and lists are slices (nil = unset, non-nil empty = explicitly cleared).
package domain
