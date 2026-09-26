//go:build race

package hostile

// raceScale multiplies Run's MaxRSS and Timeout under the race detector, whose
// shadow memory and instrumentation cost 5-10x the memory and 2-20x the time
// of a plain build (https://go.dev/doc/articles/race_detector#Runtime_Overheads).
// Assertions inside fn are not scaled.
const raceScale = 8
