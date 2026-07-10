package domain

// LoginStatus is the status returned by AuthService.Login. Every successful
// login (one or more memberships) returns SELECT_WORKSPACE; the caller must
// complete authentication via SelectWorkspace.
const LoginStatusSelectWorkspace = "SELECT_WORKSPACE"
