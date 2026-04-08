package com.example;

public interface Cacheable<T> {
    String cacheKey();

    default long ttl() {
        return 3600;
    }

    default boolean isExpired(long createdAt) {
        return System.currentTimeMillis() - createdAt > ttl() * 1000;
    }

    static <T> String buildKey(Class<T> clazz, String id) {
        return clazz.getSimpleName() + ":" + id;
    }
}
