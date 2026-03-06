export interface AuthCredentials {
  username: string
  password: string
}

export interface RegisterCredentials {
  username: string
  email: string
  password: string
}

export interface LoginResponse {
  message: string
  token: string
}
