// Package safelog suppresses upstream converter debug statements, which can
// include malformed user content. Gateway metadata logging is handled separately.
package safelog

func Printf(_ string, _ ...any) {}
