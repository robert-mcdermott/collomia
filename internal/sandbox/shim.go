package sandbox

// DispatchReexec handles the hidden process entry points used by sandbox
// backends that must re-execute the current binary before launching a command.
// It returns handled=false for ordinary application and test arguments.
func DispatchReexec(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "__landlock":
		return true, runLandlockShim(args[1:])
	case "__appcontainer":
		return true, runAppContainerShim(args[1:])
	default:
		return false, nil
	}
}
