from models import User, UserRepository


class UserService:
    def __init__(self, repo: UserRepository):
        self.repo = repo

    def create_user(self, name: str, email: str) -> User:
        user_id = f"user_{name.lower()}"
        user = User(id=user_id, name=name, email=email)
        self.repo.add(user)
        return user

    def get_user(self, user_id: str):
        return self.repo.get(user_id)

    def remove_user(self, user_id: str) -> bool:
        return self.repo.delete(user_id)
