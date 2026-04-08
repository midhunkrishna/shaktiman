pub trait Serializer {
    fn serialize(&self) -> Vec<u8>;
    fn content_type(&self) -> &str;

    fn serialize_to_string(&self) -> String {
        String::from_utf8_lossy(&self.serialize()).to_string()
    }
}

mod internal {
    pub fn validate(input: &str) -> bool {
        !input.is_empty() && input.len() < 1024
    }

    pub struct Config {
        pub host: String,
        pub port: u16,
    }

    impl Config {
        pub fn default() -> Self {
            Config {
                host: "localhost".to_string(),
                port: 8080,
            }
        }
    }
}
