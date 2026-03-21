import { Request, Response } from 'express';
import { UserService, CreateUserInput, UserRole } from '../models/user';

const userService = new UserService();

/**
 * GET /users/:id
 *
 * Responds with the requested user or 404.
 */
export async function handleGetUser(req: Request, res: Response): Promise<void> {
  const { id } = req.params;
  const user = await userService.findById(id);

  if (!user) {
    res.status(404).json({ error: `User ${id} not found` });
    return;
  }

  res.json(user);
}

/**
 * POST /users
 *
 * Creates a new user from the JSON body.
 */
export async function handleCreateUser(req: Request, res: Response): Promise<void> {
  const { name, email, role } = req.body as {
    name?: string;
    email?: string;
    role?: string;
  };

  if (!name || !email) {
    res.status(400).json({ error: 'name and email are required' });
    return;
  }

  const validRole = Object.values(UserRole).includes(role as UserRole)
    ? (role as UserRole)
    : UserRole.Viewer;

  const input: CreateUserInput = { name, email, role: validRole };

  try {
    const user = await userService.create(input);
    res.status(201).json(user);
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Internal error';
    res.status(500).json({ error: message });
  }
}

/**
 * GET /users
 *
 * Returns the full list of users.
 */
export async function handleListUsers(_req: Request, res: Response): Promise<void> {
  const users = await userService.list();
  res.json({ data: users, total: users.length });
}
