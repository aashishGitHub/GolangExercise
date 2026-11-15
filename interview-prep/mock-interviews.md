> **Lab companion:** [`mock-interviews-lab.md`](mock-interviews-lab.md) turns every issue in this question into 22 runnable examples, from the `append` race to batching a million ids.

question 1 You have this snippet in a service handling 10k req/s:

go
func (s \*Server) Process(ids []int) []Result {
var results []Result
var wg sync.WaitGroup
for \_, id := range ids {
wg.Add(1)
go func() {
defer wg.Done()
r := s.fetch(id)
results = append(results, r)
}()
}
wg.Wait()
return results
}
What’s wrong with this code? List every issue you can spot.
How would you fix it? Write the corrected version.
If ids has 1 million entries, what else concerns you and how do you handle it?

my answer
r := s.fetch(id) should catch errors and append when error !=nil
rather than returning results in an array it would be better to return as a map[int]result where key is int type
fetch is async operation so should be handled
if id has 1M entries, It should be batched and processed so we can pause or restart or play from a specific point

Answer proposed by AI

func (s \*Server) Process(ctx context.Context, ids []int) ([]Result, error) {
results := make([]Result, len(ids))
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(64) // g.Go blocks when the limit is hit, so no 1M goroutines

    for i, id := range ids {
        g.Go(func() error { // Go 1.22+: i, id are per-iteration
            r, err := s.fetch(ctx, id)
            if err != nil {
                return fmt.Errorf("fetch %d: %w", id, err)
            }
            results[i] = r // distinct index, no race
            return nil
        })
    }
    if err := g.Wait(); err != nil {
        return nil, err
    }
    return results, nil

}
