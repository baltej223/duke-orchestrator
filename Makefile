run:
	cd ../DukeDB/ ; make compile ; cp main ../duke-orchestrator/ ; cd ../duke-orchestrator/ ; go build duke_orch.go ; ./duke_orch -cf init.yaml
