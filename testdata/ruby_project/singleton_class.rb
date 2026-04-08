class Configuration
  class << self
    def instance
      @instance ||= new
    end

    def reset!
      @instance = nil
    end

    def configure
      yield instance
    end
  end

  attr_accessor :host, :port, :debug

  def initialize
    @host = "localhost"
    @port = 8080
    @debug = false
  end
end
