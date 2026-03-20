import axios from 'axios'

// Constants
export const AUTH_TOKEN_KEY = 'auth_token'
export const API_BASE_URL = import.meta.env.VITE_API_URL || ''

const client = axios.create({
  baseURL: API_BASE_URL,
  timeout: 15000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// Request interceptor with error handling
client.interceptors.request.use(
  (config) => {
    try {
      const token = localStorage.getItem(AUTH_TOKEN_KEY)
      if (token) {
        config.headers.Authorization = `Bearer ${token}`
      }
      return config
    } catch (e) {
      return Promise.reject(e)
    }
  },
  (error) => {
    return Promise.reject(error)
  }
)

// Response interceptor with improved error handling
client.interceptors.response.use(
  (response) => response,
  (error) => {
    // Handle specific error statuses
    if (error.response?.status === 401) {
      // Only redirect if not already on login page to avoid loops
      const currentPath = window.location.pathname
      if (!currentPath.includes('/login') && !currentPath.includes('/register')) {
        localStorage.removeItem(AUTH_TOKEN_KEY)
        window.location.href = '/login'
      }
    }
    
    // Enhance error message for better debugging
    if (error.response?.data?.error) {
      error.message = error.response.data.error
    }
    
    return Promise.reject(error)
  },
)

export default client
