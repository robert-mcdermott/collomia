// Package reliability holds the campaign tests that need real host resources
// rather than injected faults: a genuinely full filesystem, real signals, real
// process trees.
//
// It contains no production code. Injected faults are the right tool for
// asserting that a specific error is handled at a specific call, and the
// durable writers already have those. What they cannot show is where the
// errors actually arrive: a real full filesystem fails at write, at fsync, at
// the directory update behind a rename, and at mkdir, and it does so in an
// order no fixture author would have guessed. These tests are opt-in because
// they mount and unmount real filesystems.
package reliability
