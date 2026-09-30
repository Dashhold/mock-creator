import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { LogIn, User, Lock, AlertCircle } from 'lucide-react'

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
      {/* Background Image - Clear, No Overlay */}
      <div 
        className="absolute inset-0 bg-cover bg-center bg-no-repeat"
        style={{
          backgroundImage: 'url(/buddha-nature.jpg)',
        }}
      />

      {/* Content - Aligned to Left */}
      <div className="relative z-10 flex items-center min-h-screen p-8 pl-16">
        <div className="w-full max-w-md">
          {/* Transparent Blurry Glass Card */}
          <div className="backdrop-blur-2xl bg-white/10 rounded-3xl shadow-2xl border border-white/30 overflow-hidden">
            {/* Subtle Shine Effect */}
            <div className="absolute inset-0 bg-gradient-to-br from-white/5 via-transparent to-transparent" />
            
            {/* Card Content */}
            <div className="relative p-8">
              {/* Logo/Title Section */}
              <div className="text-center mb-8">
                <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-white/20 backdrop-blur-sm mb-4 shadow-lg">
                  <LogIn className="w-8 h-8 text-white" />
                </div>
                <h1 className="text-3xl font-bold text-white mb-2 tracking-tight drop-shadow-lg">
                  Welcome Back
                </h1>
                <p className="text-white/90 text-sm drop-shadow">
                  Sign in to continue to Mock Creator
                </p>
              </div>

              {/* Error Message */}
              {error && (
                <div className="mb-6 p-3 rounded-lg bg-red-500/30 backdrop-blur-sm border border-red-300/50 flex items-center gap-2 text-white text-sm animate-shake">
                  <AlertCircle className="w-4 h-4 flex-shrink-0" />
                  <span>{error}</span>
                </div>
              )}

              {/* Login Form */}
              <form onSubmit={handleSubmit} className="space-y-5">
                {/* Username Field */}
                <div>
                  <label htmlFor="username" className="block text-sm font-medium text-white/90 mb-2 drop-shadow">
                    Username
                  </label>
                  <div className="relative">
                    <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none">
                      <User className="h-5 w-5 text-white/60" />
                    </div>
                    <input
                      id="username"
                      type="text"
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      className="block w-full pl-10 pr-3 py-3 bg-white/15 backdrop-blur-sm border border-white/30 rounded-xl text-white placeholder-white/50 focus:outline-none focus:ring-2 focus:ring-white/40 focus:border-transparent transition-all"
                      placeholder="Enter your username"
                      required
                      autoComplete="username"
                      disabled={isLoading}
                    />
                  </div>
                </div>

                {/* Password Field */}
                <div>
                  <label htmlFor="password" className="block text-sm font-medium text-white/90 mb-2 drop-shadow">
                    Password
                  </label>
                  <div className="relative">
                    <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none">
                      <Lock className="h-5 w-5 text-white/60" />
                    </div>
                    <input
                      id="password"
                      type="password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      className="block w-full pl-10 pr-3 py-3 bg-white/15 backdrop-blur-sm border border-white/30 rounded-xl text-white placeholder-white/50 focus:outline-none focus:ring-2 focus:ring-white/40 focus:border-transparent transition-all"
                      placeholder="Enter your password"
                      required
                      autoComplete="current-password"
                      disabled={isLoading}
                    />
                  </div>
                </div>

                {/* Submit Button */}
                <button
                  type="submit"
                  disabled={isLoading}
                  className="w-full py-3 px-4 bg-white/20 backdrop-blur-sm hover:bg-white/30 text-white font-medium rounded-xl shadow-lg hover:shadow-xl transform hover:scale-[1.02] active:scale-[0.98] transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed disabled:transform-none flex items-center justify-center gap-2 border border-white/30"
                >
                  {isLoading ? (
                    <>
                      <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                      <span>Signing in...</span>
                    </>
                  ) : (
                    <>
                      <LogIn className="w-5 h-5" />
                      <span>Sign In</span>
                    </>
                  )}
                </button>
              </form>

              {/* Footer */}
              <div className="mt-6 text-center">
                <p className="text-white/70 text-xs drop-shadow">
                  Secure login powered by Mock Creator
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
