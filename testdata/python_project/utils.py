import hashlib
import json


def hash_string(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def format_user(user) -> str:
    return json.dumps({"id": user.id, "name": user.name, "email": user.email})


MAX_RETRIES = 3
DEFAULT_TIMEOUT = 30
