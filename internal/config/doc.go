// Package config builds the effective BankConfig. It holds the server defaults (from the environment),
// the field-by-field level merge (server < tenant < bank < strategy < request, and < item for retain:
// set scalars replace unset ones, models merge per slot, other lists replace), the port.ConfigResolver
// implementation that reads tenant and bank levels and slots in the matching consolidation strategy,
// and the per-RPC override validation that rejects fields an RPC can't use with INVALID_ARGUMENT.
package config
