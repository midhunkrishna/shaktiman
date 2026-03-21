package eval

// TypeScriptTestCases are curated evaluation queries for the TypeScript test fixture.
var TypeScriptTestCases = []TestCase{
	{
		Query:           "validate user token",
		ExpectedFiles:   []string{"src/auth/login.ts"},
		ExpectedSymbols: []string{"validateToken"},
		Description:     "Should find token validation function",
	},
	{
		Query:           "authentication middleware",
		ExpectedFiles:   []string{"src/auth/middleware.ts"},
		ExpectedSymbols: []string{"authGuard", "createAuthMiddleware"},
		Description:     "Should find auth middleware functions",
	},
	{
		Query:           "user service",
		ExpectedFiles:   []string{"src/models/user.ts"},
		ExpectedSymbols: []string{"UserService"},
		Description:     "Should find UserService class",
	},
	{
		Query:           "handle create user",
		ExpectedFiles:   []string{"src/api/handler.ts"},
		ExpectedSymbols: []string{"handleCreateUser"},
		Description:     "Should find user creation handler",
	},
	{
		Query:           "password hashing",
		ExpectedFiles:   []string{"src/utils/hash.ts"},
		ExpectedSymbols: []string{"hashPassword"},
		Description:     "Should find password hash utility",
	},
	{
		Query:           "refresh token",
		ExpectedFiles:   []string{"src/auth/login.ts"},
		ExpectedSymbols: []string{"refreshToken"},
		Description:     "Should find token refresh function",
	},
	{
		Query:           "user role enum",
		ExpectedFiles:   []string{"src/models/user.ts"},
		ExpectedSymbols: []string{"UserRole"},
		Description:     "Should find user role enum",
	},
	{
		Query:           "list users handler",
		ExpectedFiles:   []string{"src/api/handler.ts"},
		ExpectedSymbols: []string{"handleListUsers"},
		Description:     "Should find user listing handler",
	},
	{
		Query:           "start server",
		ExpectedFiles:   []string{"src/index.ts"},
		ExpectedSymbols: []string{"startServer"},
		Description:     "Should find server startup function",
	},
	{
		Query:           "compare password hash",
		ExpectedFiles:   []string{"src/utils/hash.ts"},
		ExpectedSymbols: []string{"comparePassword"},
		Description:     "Should find password comparison function",
	},
}
