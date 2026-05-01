import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface AuthState {
  isLoggedIn: boolean
  userId: number | null
  username: string | null
  mustChangePassword: boolean
  token: string | null

  setAuth: (token: string, userId: number, username: string, mustChangePassword: boolean) => void
  clearAuth: () => void
  setMustChangePassword: (value: boolean) => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      isLoggedIn: false,
      userId: null,
      username: null,
      mustChangePassword: false,
      token: null,

      setAuth: (token, userId, username, mustChangePassword) => {
        localStorage.setItem('token', token)
        set({
          isLoggedIn: true,
          token,
          userId,
          username,
          mustChangePassword,
        })
      },

      clearAuth: () => {
        localStorage.removeItem('token')
        set({
          isLoggedIn: false,
          token: null,
          userId: null,
          username: null,
          mustChangePassword: false,
        })
      },

      setMustChangePassword: (value) => set({ mustChangePassword: value }),
    }),
    {
      name: 'auth-storage',
    }
  )
)