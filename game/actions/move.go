package actions

import (
  "math"
  "encoding/gob"
  "path/filepath"
  "github.com/runningwild/glop/gin"
  "github.com/runningwild/glop/gui"
  "github.com/runningwild/glop/util/algorithm"
  "github.com/runningwild/haunts/base"
  "github.com/runningwild/haunts/house"
  "github.com/runningwild/haunts/game"
  "github.com/runningwild/haunts/game/status"
  "github.com/runningwild/haunts/texture"
  gl "github.com/chsc/gogl/gl21"
  lua "github.com/xenith-studios/golua"
)

func registerMoves() map[string]func() game.Action {
  move_actions := make(map[string]*MoveDef)
  base.RemoveRegistry("actions-move_actions")
  base.RegisterRegistry("actions-move_actions", move_actions)
  base.RegisterAllObjectsInDir("actions-move_actions", filepath.Join(base.GetDataDir(), "actions", "movement"), ".json", "json")
  makers := make(map[string]func() game.Action)
  for name := range move_actions {
    cname := name
    makers[cname] = func() game.Action {
      a := Move{Defname: cname}
      base.GetObject("actions-move_actions", &a)
      return &a
    }
  }
  return makers
}

var path_tex *house.LosTexture

func init() {
  game.RegisterActionMakers(registerMoves)
  gob.Register(&Move{})
  gob.Register(&moveExec{})
}

type Move struct {
  Defname string
  *MoveDef

  ent *game.Entity

  // Destination that was used to generate path
  dst int

  // Whether or not we've trid to calculate a path to the currest dst vertex.
  // Since it's possible to not find a path we might end up with a nil path
  // even if we already tried that dst, and we don't want to have to keep
  // pathing if we don't need to.
  calculated bool

  path [][2]int
  cost int

  // Ap remaining before the ability was used
  threshold int
}
type MoveDef struct {
  Name    string
  Texture texture.Object
}

type moveExec struct {
  game.BasicActionExec
  Path []int
}

func (exec *moveExec) measureCost(ent *game.Entity, g *game.Game) int {
  if len(exec.Path) == 0 {
    base.Error().Printf("Zero length path")
    return -1
  }
  if g.ToVertex(ent.Pos()) != exec.Path[0] {
    base.Error().Printf("Path doesn't begin at ent's position, %d != %d", g.ToVertex(ent.Pos()), exec.Path[0])
    return -1
  }
  base.Log().Printf("Checking %v", exec.Path)
  base.Log().Printf("Side: %d\n", ent.Side())
  graph := g.Graph(ent.Side(), nil)
  v := g.ToVertex(ent.Pos())
  base.Log().Printf("Vert: %v", v)
  cost := 0
  for _, step := range exec.Path[1:] {
    dsts, costs := graph.Adjacent(v)
    ok := false
    prev := v
    base.Log().Printf("Adj(%d):", v)
    for j := range dsts {
      base.Log().Printf("Node %d", dsts[j])
      if dsts[j] == step {
        cost += int(costs[j])
        v = dsts[j]
        ok = true
        break
      }
    }
    base.Log().Printf("%d -> %d: %t", prev, v, ok)
    if !ok {
      return -1
    }
  }
  return cost
}
func (exec *moveExec) Push(L *lua.State, g *game.Game) {
  exec.BasicActionExec.Push(L, g)
  if L.IsNil(-1) {
    return
  }
  L.PushString("Path")
  L.NewTable()
  for i := range exec.Path {
    L.PushInteger(i + 1)
    _, x, y := g.FromVertex(exec.Path[i])
    game.LuaPushPoint(L, x, y)
    L.SetTable(-3)
  }
  L.SetTable(-3)
}
func (exec *moveExec) GetPath() []int {
  return exec.Path[1:]
}
func (exec *moveExec) TruncatePath(length int) {
  exec.Path = exec.Path[0 : length+1]
}

func (a *Move) SoundMap() map[string]string {
  return nil
}

func (a *Move) Push(L *lua.State) {
  L.NewTable()
  L.PushString("Type")
  L.PushString("Move")
  L.SetTable(-3)
}

func (a *Move) AP() int {
  return a.cost
}
func (a *Move) Pos() (int, int) {
  return 0, 0
}
func (a *Move) Dims() (int, int) {
  return house.LosTextureSize, house.LosTextureSize
}
func (a *Move) String() string {
  return a.Name
}
func (a *Move) Icon() *texture.Object {
  return &a.Texture
}
func (a *Move) Readyable() bool {
  return false
}

func limitPath(ent *game.Entity, start int, path []int, max int) []int {
  total := 0
  graph := ent.Game().Graph(ent.Side(), nil)
  for last := 1; last < len(path); last++ {
    adj, cost := graph.Adjacent(start)
    for index := range adj {
      if adj[index] == path[last] {
        total += int(cost[index])
        if total >= max && last < len(path)-1 {
          return path[0 : last+1]
        }
        start = adj[index]
        break
      }
    }
  }
  return path
}

func (a *Move) AiMoveToPos(ent *game.Entity, dst []int, max_ap int) game.ActionExec {
  graph := ent.Game().Graph(ent.Side(), nil)
  src := []int{ent.Game().ToVertex(ent.Pos())}
  _, path := algorithm.Dijkstra(graph, src, dst)
  if path == nil {
    return nil
  }
  if ent.Stats.ApCur() < max_ap {
    max_ap = ent.Stats.ApCur()
  }
  path = limitPath(ent, src[0], path, max_ap)
  if len(path) <= 1 {
    return nil
  }
  var exec moveExec
  exec.SetBasicData(ent, a)
  exec.Path = path
  return &exec
}

// Attempts to move such that the shortest path from any location
// (txs[i], tys[i]) is between min_dist and max_dist.  Will not spend more
// than max_ap Ap doing this.
func (a *Move) AiMoveInRange(ent *game.Entity, targets []*game.Entity, min_dist, max_dist, max_ap int) game.ActionExec {
  graph := ent.Game().Graph(ent.Side(), targets)
  var src []int
  for i := range targets {
    src = append(src, ent.Game().ToVertex(targets[i].Pos()))
  }
  dst := algorithm.ReachableWithinBounds(graph, src, float64(min_dist), float64(max_dist))
  if len(dst) == 0 {
    return nil
  }

  source_cell := []int{ent.Game().ToVertex(ent.Pos())}
  _, path := algorithm.Dijkstra(graph, source_cell, dst)
  if path == nil {
    return nil
  }
  if ent.Stats.ApCur() > max_ap {
    max_ap = ent.Stats.ApCur()
  }
  path = limitPath(ent, source_cell[0], path, max_ap)
  if len(path) <= 1 {
    return nil
  }
  var exec moveExec
  exec.SetBasicData(ent, a)
  exec.Path = path
  return &exec
}

func (a *Move) AiCostToMoveInRange(ent *game.Entity, targets []*game.Entity, min_dist, max_dist int) int {
  graph := ent.Game().Graph(ent.Side(), targets)
  var src []int
  for i := range targets {
    src = append(src, ent.Game().ToVertex(targets[i].Pos()))
  }
  dst := algorithm.ReachableWithinBounds(graph, src, float64(min_dist), float64(max_dist))
  if len(dst) == 0 {
    return 0
  }

  source_cell := []int{ent.Game().ToVertex(ent.Pos())}
  cost, path := algorithm.Dijkstra(graph, source_cell, dst)
  if path == nil {
    return -1
  }
  return int(cost)
}

func (a *Move) findPath(ent *game.Entity, x, y int) {
  g := ent.Game()
  dst := g.ToVertex(x, y)
  if dst != a.dst || !a.calculated {
    a.dst = dst
    a.calculated = true
    src := g.ToVertex(a.ent.Pos())
    graph := g.Graph(ent.Side(), nil)
    cost, path := algorithm.Dijkstra(graph, []int{src}, []int{dst})
    if len(path) <= 1 {
      return
    }
    a.path = algorithm.Map(path, [][2]int{}, func(a interface{}) interface{} {
      _, x, y := g.FromVertex(a.(int))
      return [2]int{int(x), int(y)}
    }).([][2]int)
    a.cost = int(cost)

    if path_tex != nil {
      pix := path_tex.Pix()
      for i := range pix {
        for j := range pix[i] {
          pix[i][j] = 0
        }
      }
      current := 0.0
      for i := 1; i < len(a.path); i++ {
        src := g.ToVertex(a.path[i-1][0], a.path[i-1][1])
        dst := g.ToVertex(a.path[i][0], a.path[i][1])
        v, cost := graph.Adjacent(src)
        for j := range v {
          if v[j] == dst {
            current += cost[j]
            break
          }
        }
        pix[a.path[i][1]][a.path[i][0]] += byte(current)
      }
      path_tex.Remap()
    }
  }
}

func (a *Move) Preppable(ent *game.Entity, g *game.Game) bool {
  return true
}
func (a *Move) Prep(ent *game.Entity, g *game.Game) bool {
  a.ent = ent
  fx, fy := g.GetViewer().WindowToBoard(gin.In().GetCursor("Mouse").Point())
  a.findPath(ent, int(fx), int(fy))
  a.threshold = a.ent.Stats.ApCur()
  return true
}
func (a *Move) HandleInput(group gui.EventGroup, g *game.Game) (bool, game.ActionExec) {
  cursor := group.Events[0].Key.Cursor()
  if cursor != nil {
    fx, fy := g.GetViewer().WindowToBoard(cursor.Point())
    a.findPath(a.ent, int(fx), int(fy))
  }
  if found, _ := group.FindEvent(gin.MouseLButton); found {
    if len(a.path) > 0 {
      if a.cost <= a.ent.Stats.ApCur() {
        var exec moveExec
        exec.SetBasicData(a.ent, a)
        algorithm.Map2(a.path, &exec.Path, func(v [2]int) int {
          return g.ToVertex(v[0], v[1])
        })
        return true, &exec
      }
      return true, nil
    } else {
      return false, nil
    }
  }
  return false, nil
}
func (a *Move) RenderOnFloor() {
  if a.ent == nil {
    return
  }
  if path_tex == nil {
    path_tex = house.MakeLosTexture()
  }
  path_tex.Remap()
  path_tex.Bind()
  gl.Color4ub(255, 255, 255, 128)
  base.EnableShader("path")
  base.SetUniformF("path", "threshold", float32(a.threshold)/255)
  base.SetUniformF("path", "size", house.LosTextureSize)
  texture.RenderAdvanced(0, 0, house.LosTextureSize, house.LosTextureSize, 3.1415926535, false)
  base.EnableShader("")
}
func (a *Move) Cancel() {
  a.ent = nil
  a.path = nil
  a.calculated = false
}
func (a *Move) Maintain(dt int64, g *game.Game, ae game.ActionExec) game.MaintenanceStatus {
  if ae != nil {
    exec := ae.(*moveExec)
    a.ent = g.EntityById(ae.EntityId())
    if len(exec.Path) == 0 {
      base.Error().Printf("Got a move exec with a path length of 0: %v", exec)
      return game.Complete
    }
    a.cost = exec.measureCost(a.ent, g)
    if a.cost > a.ent.Stats.ApCur() {
      base.Error().Printf("Got a move that required more ap than available: %v", exec)
      base.Error().Printf("Path: %v", exec.Path)
      return game.Complete
    }
    if a.cost == -1 {
      base.Error().Printf("Got a move that followed an invalid path: %v", exec)
      base.Error().Printf("Path: %v", exec.Path)
      if a.ent == nil {
        base.Error().Printf("ENT was Nil!")
      } else {
        x, y := a.ent.Pos()
        v := g.ToVertex(x, y)
        base.Error().Printf("Ent pos: (%d, %d) -> (%d)", x, y, v)
      }
      return game.Complete
    }
    algorithm.Map2(exec.Path, &a.path, func(v int) [2]int {
      _, x, y := g.FromVertex(v)
      return [2]int{x, y}
    })
    base.Log().Printf("Path Validated: %v", exec)
    a.ent.Stats.ApplyDamage(-a.cost, 0, status.Unspecified)
  }
  // Do stuff
  factor := float32(math.Pow(2, a.ent.Walking_speed))
  dist := a.ent.DoAdvance(factor*float32(dt)/200, a.path[0][0], a.path[0][1])
  for dist > 0 {
    if len(a.path) == 1 {
      a.ent.DoAdvance(0, 0, 0)
      a.ent.Info.RoomsExplored[a.ent.CurrentRoom()] = true
      a.ent = nil
      return game.Complete
    }
    a.path = a.path[1:]
    a.ent.Info.RoomsExplored[a.ent.CurrentRoom()] = true
    dist = a.ent.DoAdvance(dist, a.path[0][0], a.path[0][1])
  }
  return game.InProgress
}
func (a *Move) Interrupt() bool {
  return true
}
