# frozen_string_literal: true

require 'digest'
require 'json'

# Utility module for common operations
module Utils
  MAX_RETRIES = 3
  DEFAULT_TIMEOUT = 30

  def self.hash_string(value)
    Digest::SHA256.hexdigest(value)
  end

  def self.format_user(user)
    JSON.generate(user.to_h)
  end

  def self.retry_with_backoff(max_retries: MAX_RETRIES)
    retries = 0
    begin
      yield
    rescue StandardError => e
      retries += 1
      raise e if retries >= max_retries

      sleep(2**retries)
      retry
    end
  end
end
