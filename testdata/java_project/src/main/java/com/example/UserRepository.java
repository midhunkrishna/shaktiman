package com.example;

import java.util.List;
import java.util.Optional;

/**
 * Repository interface for user persistence.
 */
public interface UserRepository {
    void save(User user);
    Optional<User> findById(String id);
    List<User> findAll();
    boolean deleteById(String id);
}
