package solvers

import (
	"math"

	"github.com/chewxy/math32"
	"github.com/pkg/errors"
	"gorgonia.org/gorgonia/values"
	"gorgonia.org/tensor"
)

// RMSPropSolver is a solver that implements Geoffrey Hinton's RMSProp gradient descent optimization algorithm.
// http://www.cs.toronto.edu/~tijmen/csc321/slides/lecture_slides_lec6.pdf
type RMSPropSolver struct {
	decay float64 // decay rate/rho
	eps   float64 // smoothing factor
	l2reg float64 // l2 regularization
	clip  float64 // clip value
	eta   float64 // learn rate

	useClip, useL2Reg bool

	// unsettable
	cache []*dualValue
}

// NewRMSPropSolver creates an RMSProp solver with these default values:
//		eta (learn rate)	  : 0.001
//		eps (smoothing factor): 1e-8
//		rho (decay factor)    : 0.999
func NewRMSPropSolver(opts ...SolverOpt) *RMSPropSolver {
	s := &RMSPropSolver{
		decay: 0.999,
		eps:   1e-8,
		eta:   0.001,
	}

	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Step steps through each node in the model and applies the RMSProp gradient descent algorithm on the value.
//
// This function will error out if the nodes do not have an associated Grad value.
func (s *RMSPropSolver) Step(model []ValueGrad) (err error) {
	if s.cache == nil {
		s.cache = make([]*values.Dual, len(model))
	}

	for i, n := range model {
		var weights, grad Value
		if weights, grad, err = extractWeightGrad(n); err != nil {
			return err
		}

		var cached *values.Dual
		if cached = s.cache[i]; cached == nil {
			if cached, err = newCachedDV(n, weights, grad, true); err != nil {
				return err
			}
			s.cache[i] = cached
		}

		cv := cached.Value
		// cw = cw*decay + (1-decay) * grad²
		switch cw := cv.(type) {
		case *tensor.Dense:
			var gt, gt2, w, regularized tensor.Tensor
			var decay, omdecay, stepSize, eps, l2reg, clip, negClip interface{}
			switch cw.Dtype() {
			case tensor.Float64:
				decay = s.decay
				omdecay = 1.0 - s.decay
				stepSize = -s.eta
				eps = s.eps
				l2reg = s.l2reg
				clip = s.clip
				negClip = -s.clip
			case tensor.Float32:
				decay = float32(s.decay)
				omdecay = float32(1.0 - s.decay)
				stepSize = float32(-s.eta)
				eps = float32(s.eps)
				l2reg = float32(s.l2reg)
				clip = float32(s.clip)
				negClip = float32(-s.clip)
			}

			gt = grad.(tensor.Tensor)
			if gt2, err = tensor.Square(gt); err != nil {
				return errors.Wrap(err, pointWiseSquareFail)
			}
			tensor.Mul(cw, decay, tensor.UseUnsafe())
			tensor.Mul(gt2, omdecay, tensor.UseUnsafe())
			tensor.Add(cw, gt2, tensor.UseUnsafe())
			defer returnTensor(gt2)

			if s.useClip {
				if _, err = tensor.Clamp(gt, negClip, clip, tensor.UseUnsafe()); err != nil {
					return errors.Wrap(err, clampFail)
				}
			}

			// regularize
			var upd tensor.Tensor
			if upd, err = tensor.Add(cw, eps); err != nil {
				return errors.Wrap(err, "Failed to carry Add()")
			}

			if _, err = tensor.InvSqrt(upd, tensor.UseUnsafe()); err != nil {
				return errors.Wrap(err, invSqrtFail)
			}
			if _, err = tensor.Mul(gt, stepSize, tensor.UseUnsafe()); err != nil {
				return errors.Wrap(err, pointWiseMulFail)
			}
			if _, err = tensor.Mul(upd, gt, tensor.UseUnsafe()); err != nil {
				return errors.Wrap(err, pointWiseMulFail)
			}

			// update
			w = weights.(*tensor.Dense)
			if s.useL2Reg {
				if regularized, err = tensor.Mul(w, l2reg); err != nil {
					return errors.Wrap(err, pointWiseMulFail)
				}
				if _, err = tensor.Sub(upd, regularized, tensor.UseUnsafe()); err != nil {
					return errors.Wrap(err, subFail)
				}
				defer returnTensor(regularized)
			}

			if _, err = tensor.Add(w, upd, tensor.UseUnsafe()); err != nil {
				return errors.Wrap(err, addFail)
			}
			defer returnTensor(upd)

			// zero all
			gt.Zero()

		case *F32:
			decay := float32(s.decay)
			omdecay := float32(1.0 - s.decay)
			stepSize := float32(s.eta)
			eps := float32(s.eps)
			l2reg := float32(s.l2reg)

			gs := grad.(*F32).any()
			c := cw.any()
			c = c*decay + omdecay*gs*gs

			cached.Value, _ = anyToScalar(c)

			w := weights.(*F32).any()
			upd := -stepSize*gs/math32.Sqrt(c+eps) - l2reg*w
			w += upd

			// because scalar values are copies, and not pointers, we have to actually re-update the dualValu in model[i]
			*(weights.(*F32)) = F32(w)
			*(grad.(*F32)) = F32(0.0)
		case *F64:
			decay := s.decay
			omdecay := 1.0 - s.decay
			stepSize := s.eta
			eps := s.eps
			l2reg := s.l2reg

			gs := grad.(*F64).any()
			c := cw.any()
			c = c*decay + omdecay*gs*gs

			cached.Value, _ = anyToScalar(c)

			w := weights.(*F64).any()
			upd := -stepSize*gs/math.Sqrt(c+eps) - l2reg*w
			w += upd

			// because scalar values are copies, and not pointers, we have to actually re-update the dualValu in model[i]
			*(weights.(*F64)) = F64(w)
			*(grad.(*F64)) = F64(0.0)
		default:
		}
		solverLogf("AFTER %1.1s", n)
	}
	return nil
}