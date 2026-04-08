extern "C" {
    fn abs(input: i32) -> i32;
    fn strlen(s: *const std::os::raw::c_char) -> usize;
    static errno: i32;
}

union FloatOrInt {
    f: f32,
    i: u32,
}

pub fn safe_abs(x: i32) -> i32 {
    unsafe { abs(x) }
}
