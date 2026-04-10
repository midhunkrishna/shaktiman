class Outer:
    class Inner:
        def __init__(self, value):
            self.value = value

        def process(self):
            return self.value * 2

    def __init__(self):
        self.inner = self.Inner(42)

    def run(self):
        return self.inner.process()
