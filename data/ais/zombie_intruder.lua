ps = Utils.AllPathablePoints(Me.Pos, Me.Pos, 1, 5)
r = randN(table.getn(ps))
target = ps[randN(table.getn(ps))]
a = {}
a[1] = target
res = Do.Move(a, 1000)
