package com.example;

import java.util.List;
import java.util.ArrayList;
import java.util.Optional;

/**
 * Service for managing users.
 */
public class UserService {
    private final List<User> users = new ArrayList<>();

    public UserService() {
    }

    public void addUser(User user) {
        users.add(user);
    }

    public Optional<User> findById(String id) {
        return users.stream()
            .filter(u -> u.getId().equals(id))
            .findFirst();
    }

    public List<User> getAllUsers() {
        return List.copyOf(users);
    }

    public boolean removeUser(String id) {
        return users.removeIf(u -> u.getId().equals(id));
    }
}
