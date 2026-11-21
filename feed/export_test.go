package feed

func SnapshotRuntimeForTest() (int32, bool) { return runtimeSnapshot() }

func RestoreRuntimeForTest(mode int32, reads bool) { restoreRuntime(mode, reads) }
