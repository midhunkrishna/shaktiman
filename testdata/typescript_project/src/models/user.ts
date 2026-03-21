/** Available roles for access control. */
export enum UserRole {
  Admin = 'admin',
  Editor = 'editor',
  Viewer = 'viewer',
}

/** Core user entity. */
export interface User {
  id: string;
  name: string;
  email: string;
  role: UserRole;
  createdAt: Date;
}

/** Fields accepted when creating a new user. */
export type CreateUserInput = Omit<User, 'id' | 'createdAt'>;

/** Fields accepted when updating an existing user. */
export type UpdateUserInput = Partial<Omit<User, 'id' | 'createdAt'>>;

/**
 * In-memory user store.
 *
 * In production this would delegate to a database client.
 */
export class UserService {
  private users: Map<string, User> = new Map();

  /** Retrieve a user by ID, or `undefined` if not found. */
  async findById(id: string): Promise<User | undefined> {
    return this.users.get(id);
  }

  /** Persist a new user and return the stored entity. */
  async create(input: CreateUserInput): Promise<User> {
    const id = crypto.randomUUID();
    const user: User = {
      id,
      ...input,
      createdAt: new Date(),
    };
    this.users.set(id, user);
    return user;
  }

  /** Apply a partial update to an existing user. */
  async update(id: string, input: UpdateUserInput): Promise<User> {
    const existing = this.users.get(id);
    if (!existing) {
      throw new Error(`User not found: ${id}`);
    }

    const updated: User = { ...existing, ...input };
    this.users.set(id, updated);
    return updated;
  }

  /** Remove a user. Throws if the user does not exist. */
  async delete(id: string): Promise<void> {
    if (!this.users.has(id)) {
      throw new Error(`User not found: ${id}`);
    }
    this.users.delete(id);
  }

  /** Return all users as an array. */
  async list(): Promise<User[]> {
    return Array.from(this.users.values());
  }
}
