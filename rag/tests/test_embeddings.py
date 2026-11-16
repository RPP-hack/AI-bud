import math
import unittest

from rag.embeddings import EmbeddingError, normalize_vector


class EmbeddingTests(unittest.TestCase):
    def test_normalizes_dense_vectors(self):
        self.assertEqual(normalize_vector([3, 4], 2), [0.6, 0.8])
        vector = normalize_vector([1, -2, 3], 3)
        self.assertAlmostEqual(math.hypot(*vector), 1)
        self.assertLess(vector[1], 0)

    def test_rejects_invalid_vectors(self):
        for vector in ([0, 0], [1], [True, 1], [float("nan"), 1], [float("inf"), 1],
                       [10 ** 500, 1], ["x", 1], None):
            with self.subTest(vector=vector), self.assertRaises(EmbeddingError):
                normalize_vector(vector, 2)


if __name__ == "__main__":
    unittest.main()
