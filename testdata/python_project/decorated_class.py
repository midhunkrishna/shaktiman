class CacheManager:
    def __init__(self, max_size=100):
        self._cache = {}
        self._max_size = max_size

    @property
    def size(self):
        return len(self._cache)

    @staticmethod
    def hash_key(key):
        import hashlib
        return hashlib.sha256(key.encode()).hexdigest()

    @classmethod
    def create_with_defaults(cls):
        return cls(max_size=256)

    def get(self, key):
        return self._cache.get(self.hash_key(key))

    def set(self, key, value):
        if len(self._cache) >= self._max_size:
            self._evict()
        self._cache[self.hash_key(key)] = value

    def _evict(self):
        oldest = next(iter(self._cache))
        del self._cache[oldest]
