import express from 'express';
import { createAuthMiddleware } from './auth/middleware';
import { handleGetUser, handleCreateUser, handleListUsers } from './api/handler';
import { UserRole } from './models/user';
import { hashPassword } from './utils/hash';

export { UserRole } from './models/user';
export { validateToken, refreshToken } from './auth/login';
export type { TokenPayload } from './auth/login';
export type { AuthConfig } from './auth/middleware';

/** Default port when no `PORT` environment variable is set. */
export const DEFAULT_PORT = 3000;

/**
 * Bootstrap the Express application and begin listening.
 *
 * @param port - TCP port to bind to.
 */
export function startServer(port: number = DEFAULT_PORT): void {
  const app = express();

  app.use(express.json());

  // Auth middleware with public health-check paths
  const auth = createAuthMiddleware({
    publicPaths: ['/health', '/readiness'],
    headerName: 'authorization',
    attachPayload: true,
  });
  app.use(auth);

  // Health check
  app.get('/health', (_req, res) => {
    res.json({ status: 'ok' });
  });

  // User routes
  app.get('/users', handleListUsers);
  app.get('/users/:id', handleGetUser);
  app.post('/users', handleCreateUser);

  app.listen(port, () => {
    console.log(`Server listening on port ${port}`);
  });
}

// Auto-start when executed directly
if (require.main === module) {
  const port = parseInt(process.env.PORT ?? '', 10) || DEFAULT_PORT;
  startServer(port);
}
