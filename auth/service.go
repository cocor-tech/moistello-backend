// auth/service.go
type RegisterResponse struct {
    UserID  string `json:"user_id"`
    Message string `json:"message"`
    // OTP field removed completely
}

func (s *AuthService) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
    otp := generateOTP()

    if s.cfg.Environment == "development" {
        // Log to console/stdout for local testing, NEVER put in API response
        log.Printf("[DEV ONLY] Generated OTP for user %s: %s", req.Email, otp)
    } else {
        s.emailService.SendOTP(ctx, req.Email, otp)
    }

    return &RegisterResponse{
        UserID:  user.ID,
        Message: "Registration successful. Please check your email for the OTP.",
    }, nil
}