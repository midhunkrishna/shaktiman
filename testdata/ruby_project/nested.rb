module Authentication
  class TokenValidator
    def initialize(secret)
      @secret = secret
    end

    def validate(token)
      decoded = decode(token)
      decoded && !expired?(decoded)
    end

    private

    def decode(token)
      Base64.decode64(token)
    rescue StandardError
      nil
    end

    def expired?(payload)
      Time.now.to_i > payload[:exp]
    end
  end

  class TokenGenerator
    def initialize(secret, ttl: 3600)
      @secret = secret
      @ttl = ttl
    end

    def generate(payload)
      data = payload.merge(exp: Time.now.to_i + @ttl)
      Base64.encode64(data.to_json)
    end
  end
end
