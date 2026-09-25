package apperrors

import "errors"

var (
	ErrNotFound                 = errors.New("resource not found")
	ErrUnauthorized             = errors.New("unauthorized")
	ErrForbidden                = errors.New("forbidden")
	ErrInvalidInput             = errors.New("invalid input")
	ErrConflict                 = errors.New("resource already exists")
	ErrInternal                 = errors.New("internal server error")
	ErrWalletNotVerified        = errors.New("wallet signature verification failed")
	ErrTokenExpired             = errors.New("token expired")
	ErrNonceExpired             = errors.New("nonce expired")
	ErrCircleFull               = errors.New("circle is full")
	ErrCircleNotActive          = errors.New("circle is not active")
	ErrAlreadyMember            = errors.New("already a member of this circle")
	ErrNotOrganizer             = errors.New("only the organizer can perform this action")
	ErrMoiScoreTooLow           = errors.New("MoiScore is too low for this circle")
	ErrInvalidInvite            = errors.New("invalid or expired invite code")
	ErrMaxStrikes               = errors.New("maximum strikes reached")
	ErrDuplicateFile            = errors.New("file with this name already exists")
	ErrLateContributionRejected = errors.New("contributions rejected: payout scheduling has already begun for this round")
)

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string { return "validation failed" }
