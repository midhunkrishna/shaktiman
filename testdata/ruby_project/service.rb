# frozen_string_literal: true

require_relative 'models'

# Service layer for user management
class UserService
  def initialize(repo)
    @repo = repo
  end

  def create_user(name:, email:)
    user_id = "user_#{name.downcase.gsub(/\s+/, '_')}"
    user = User.new(id: user_id, name: name, email: email)
    @repo.add(user)
    user
  end

  def get_user(user_id)
    @repo.get(user_id)
  end

  def remove_user(user_id)
    @repo.delete(user_id)
  end

  def list_users
    @repo.all
  end

  def self.create_admin(repo, name:, email:)
    user_id = "admin_#{name.downcase.gsub(/\s+/, '_')}"
    user = User.new(id: user_id, name: name, email: email, role: 'admin')
    repo.add(user)
    user
  end
end
