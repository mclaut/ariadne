package backupretention

// renameExclusive uses MOVEFILE_WRITE_THROUGH; each data file is flushed first.
func syncDirectory(_ string) error { return nil }
