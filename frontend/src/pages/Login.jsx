import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AlertCircle } from 'lucide-react'

const CREDENTIALS = {
  username: 'dreambig',
  password: '7011091643'
}

export default function Login({ onLogin }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const navigate = useNavigate()

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    setIsLoading(true)

    // Simulate network delay for smooth UX
    await new Promise(resolve => setTimeout(resolve, 800))

    if (username === CREDENTIALS.username && password === CREDENTIALS.password) {
      localStorage.setItem('isAuthenticated', 'true')
      onLogin()
      navigate('/dashboard')
    } else {
      setError('Invalid username or password')
      setIsLoading(false)
    }
  }

  return (
    <div className="min-h-screen w-full relative overflow-hidden">
      {/* Clear Background Image */}
      <div 
        className="absolute inset-0 bg-cover bg-center bg-no-repeat"
        style={{
          backgroundImage: 'url(/bg-1.png)',
        }}
      />

      {/* Minimal Login Form - Responsive positioning */}
      <div className="relative z-10 p-4 sm:p-8 sm:pt-12 sm:pl-12 flex items-center justify-center sm:justify-start min-h-screen">
        <form onSubmit={handleSubmit} className="w-full max-w-sm sm:w-80 space-y-4">
          {/* Error Message */}
          {error && (
            <div className="p-3 rounded-lg bg-red-500/80 backdrop-blur-sm flex items-center gap-2 text-white text-sm animate-shake">
              <AlertCircle className="w-4 h-4 flex-shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {/* Username Field */}
          <div>
            <input
              id="username"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="block w-full px-4 py-3 bg-white/90 backdrop-blur-sm border-0 rounded-lg text-gray-800 placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-white/50 transition-all shadow-lg"
              placeholder="Username"
              required
              autoComplete="username"
              disabled={isLoading}
            />
          </div>

          {/* Password Field */}
          <div>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="block w-full px-4 py-3 bg-white/90 backdrop-blur-sm border-0 rounded-lg text-gray-800 placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-white/50 transition-all shadow-lg"
              placeholder="Password"
              required
              autoComplete="current-password"
              disabled={isLoading}
            />
          </div>

          {/* Submit Button */}
          <button
            type="submit"
            disabled={isLoading}
            className="w-full py-3 px-4 bg-white/90 backdrop-blur-sm hover:bg-white text-gray-800 font-medium rounded-lg shadow-lg hover:shadow-xl transform hover:scale-[1.02] active:scale-[0.98] transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed disabled:transform-none"
          >
            {isLoading ? (
              <div className="flex items-center justify-center gap-2">
                <div className="w-5 h-5 border-2 border-gray-400 border-t-gray-800 rounded-full animate-spin" />
                <span>Signing in...</span>
              </div>
            ) : (
              <span>Sign In</span>
            )}
          </button>
        </form>
      </div>
    </div>
  )
}
