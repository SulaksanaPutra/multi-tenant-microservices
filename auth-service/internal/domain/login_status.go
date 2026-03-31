package domain

// LoginStatus values returned by AuthService.Login to tell the caller
// whether login completed with a token pair or requires workspace selection.
const (
	LoginStatusSuccess         = "SUCCESS"
	LoginStatusSelectWorkspace = "SELECT_WORKSPACE"
)
