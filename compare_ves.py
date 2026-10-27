import numpy as np
from ertkit.ves import LayeredModel, Survey, forward

model = LayeredModel(
    resistivity=np.array([100.0, 10.0, 500.0]),
    thickness=np.array([5.0, 15.0]),
)

ab2 = np.array(
    [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000],
    dtype=float,
)

# Small, fixed MN/2 for the reference survey.
mn2 = 0.1

survey = Survey.schlumberger(ab2=ab2, mn2=mn2)
rhoa = forward(model, survey)

print("Three-layer Schlumberger VES reference")
print("Resistivities (ohm-m): [100, 10, 500]")
print("Thicknesses (m):      [5, 15]")
print(f"MN/2 (m):              {mn2}")
print()
print(f"{'AB/2 (m)':>12} {'Rhoa (ohm-m)':>18}")
print("-" * 32)

for spacing, value in zip(ab2, rhoa):
    print(f"{spacing:12.1f} {value:18.6f}")
