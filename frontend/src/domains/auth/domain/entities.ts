export interface AuthCredentials {
  username: string
  password: string
  captchaResponse?: string
}

export interface RegisterCredentials {
  username: string
  email: string
  password: string
  passwordConfirm?: string
  displayName?: string
  inviteCode?: string
  captchaResponse?: string
}

export interface LoginResponse {
  message: string
  token: string
}

/** Password accepted, but the account needs a TOTP or backup code. */
export interface TwoFactorChallenge {
  twoFactorRequired: true
  challenge: string
}
