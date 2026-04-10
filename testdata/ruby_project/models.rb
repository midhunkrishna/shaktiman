# frozen_string_literal: true

# User model representing a system user
class User
  attr_accessor :id, :name, :email, :role

  def initialize(id:, name:, email:, role: 'user')
    @id = id
    @name = name
    @email = email
    @role = role
  end

  def admin?
    role == 'admin'
  end

  def to_h
    { id: id, name: name, email: email, role: role }
  end
end

# Post model representing a blog post
class Post
  attr_accessor :id, :title, :content, :author_id, :published

  def initialize(id:, title:, content:, author_id:, published: false)
    @id = id
    @title = title
    @content = content
    @author_id = author_id
    @published = published
  end

  def publish!
    @published = true
  end
end

# Repository for managing User objects
class UserRepository
  def initialize
    @users = {}
  end

  def add(user)
    @users[user.id] = user
  end

  def get(user_id)
    @users[user_id]
  end

  def delete(user_id)
    return false unless @users.key?(user_id)

    @users.delete(user_id)
    true
  end

  def all
    @users.values
  end
end
