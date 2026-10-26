package environments

// Recovery diagnostics use a closed vocabulary. Raw engine/provider causes may
// contain credentials, tokens or private host paths and must never reach these.
type recoveryPhase string

const (
	phaseFolder         recoveryPhase = "folder-create"
	phaseBridgeInspect  recoveryPhase = "bridge-inspect"
	phaseBridgeIdentity recoveryPhase = "bridge-identity"
	phaseBridgeCreate   recoveryPhase = "bridge-create"
	phaseInspect        recoveryPhase = "consumer-inspect"
	phaseIdentity       recoveryPhase = "consumer-identity"
	phaseRestartPolicy  recoveryPhase = "consumer-restart-policy"
	phaseStop           recoveryPhase = "consumer-stop"
	phaseRemove         recoveryPhase = "consumer-remove"
	phaseRuntimeRemove  recoveryPhase = "runtime-remove"
	phaseEnsure         recoveryPhase = "runtime-ensure"
	phaseConnection     recoveryPhase = "connection-read"
	phaseSnapshot       recoveryPhase = "snapshot-write"
	phaseDefinition     recoveryPhase = "definition-read"
	phaseCreate         recoveryPhase = "consumer-create"
	phaseStart          recoveryPhase = "consumer-start"
	phaseReady          recoveryPhase = "local-ready"
)
