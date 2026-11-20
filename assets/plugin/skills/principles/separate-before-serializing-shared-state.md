# Separate before serializing shared state

First ask whether they need one shared object or are publishing independent facts. Default to
giving each actor its own file, key, branch or directory, and merge where the state is read.
Serialize access (a lock, a single writer, sequential phases, compare-and-swap) only when one
shared target is a real invariant. Instructions are not concurrency control.
