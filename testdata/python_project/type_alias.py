type Point = tuple[float, float]
type Matrix[T] = list[list[T]]
type Handler = Callable[[Request], Response]

def distance(a: Point, b: Point) -> float:
    return ((a[0] - b[0]) ** 2 + (a[1] - b[1]) ** 2) ** 0.5
