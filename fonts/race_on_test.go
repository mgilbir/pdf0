//go:build race

package fonts

// raceEnabled says whether the race detector is on, so a test whose whole
// meaning is "the detector saw nothing" can skip loudly instead of reporting a
// pass it never established.
const raceEnabled = true
