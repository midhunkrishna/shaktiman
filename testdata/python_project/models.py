from dataclasses import dataclass
from typing import Optional


@dataclass
class User:
    id: str
    name: str
    email: str
    role: str = "user"


@dataclass
class Post:
    id: str
    title: str
    content: str
    author_id: str
    published: bool = False


class UserRepository:
    def __init__(self):
        self.users = {}

    def add(self, user: User) -> None:
        self.users[user.id] = user

    def get(self, user_id: str) -> Optional[User]:
        return self.users.get(user_id)

    def delete(self, user_id: str) -> bool:
        if user_id in self.users:
            del self.users[user_id]
            return True
        return False
