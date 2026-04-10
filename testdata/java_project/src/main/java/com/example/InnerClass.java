package com.example;

public class Outer {
    private String name;

    public Outer(String name) {
        this.name = name;
    }

    public String getName() {
        return name;
    }

    public class Inner {
        private int value;

        public Inner(int value) {
            this.value = value;
        }

        public int getValue() {
            return value;
        }

        public String describe() {
            return name + ": " + value;
        }
    }

    public static class StaticNested {
        public static String greet() {
            return "Hello from StaticNested";
        }
    }
}
