package gohan

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// titleCostFeature is the cost tag the title child run carries.
const titleCostFeature = "title"

// titleInstruction is the one model-facing sentence the trigger adds
// ahead of the conversation blocks.
const titleInstruction = "Write a title for this conversation in at most six words. Answer with a JSON string."

// maybeTitle starts the title child run after the first Done of a
// session's first primary run. The eligibility checks run in the caller's
// context; the generation runs detached, so it never delays the primary
// Done.
func (c *conversation) maybeTitle(ctx context.Context, sessionID string) {
	if c.titleProfile == "" || c.recipeModel == nil {
		return
	}
	principal, hasPrincipal := types.PrincipalFrom(ctx)
	pending, err := c.titlePending(ctx, sessionID)
	if err != nil || !pending {
		return
	}
	tctx := types.WithRunInfo(context.WithoutCancel(ctx), types.RunInfo{
		Flow:        c.spec,
		SessionID:   sessionID,
		ParentRunID: sessionID,
		Principal:   principal,
		CostTags:    types.CostTags{Feature: titleCostFeature},
	})
	if hasPrincipal {
		tctx = types.WithPrincipal(tctx, principal)
	}
	c.markTitled(sessionID)
	go c.generateTitle(tctx, sessionID)
}

func (c *conversation) markTitled(sessionID string) {
	c.titleMu.Lock()
	defer c.titleMu.Unlock()
	if c.titled == nil {
		c.titled = map[string]bool{}
	}
	c.titled[sessionID] = true
}

// titlePending reports whether the session is a primary still awaiting its
// first title: no title, not locked, and never generated here.
func (c *conversation) titlePending(ctx context.Context, sessionID string) (bool, error) {
	c.titleMu.Lock()
	done := c.titled[sessionID]
	c.titleMu.Unlock()
	if done {
		return false, nil
	}
	idx, ok := c.log.(stores.SessionIndex)
	if !ok {
		return false, nil
	}
	ownerFinder, ok := c.log.(interface {
		Owner(ctx context.Context, sessionID string) (types.SessionOwner, error)
	})
	if !ok {
		return false, nil
	}
	owner, err := ownerFinder.Owner(ctx, sessionID)
	if err != nil {
		return false, err
	}
	metas, _, err := idx.Sessions(ctx, owner, stores.SessionQuery{Kinds: []stores.SessionKind{stores.SessionPrimary}})
	if err != nil {
		return false, err
	}
	for _, m := range metas {
		if m.ID != sessionID {
			continue
		}
		return m.Title == "" && !m.TitleLocked && m.Kind == stores.SessionPrimary, nil
	}
	return false, nil
}

// generateTitle runs one child run of the primary run and writes the title
// with UpdateSession. The write locks the title, so a rename always wins
// and a session is titled at most once.
func (c *conversation) generateTitle(ctx context.Context, sessionID string) {
	h, err := c.load(ctx, sessionID)
	if err != nil {
		return
	}
	blocks := titleInput(h.Messages)
	if len(blocks) == 0 {
		return
	}
	title, err := c.titleCall(ctx, blocks)
	if err != nil || title == "" {
		return
	}
	if idx, ok := c.log.(stores.SessionIndex); ok {
		_ = idx.UpdateSession(ctx, sessionID, stores.SessionPatch{Title: &title})
	}
}

// titleCall makes one governed model call over the profile the stack
// resolved for titles and decodes the JSON-string answer.
func (c *conversation) titleCall(ctx context.Context, blocks []types.Block) (string, error) {
	rc, err := c.recipeModel(c.titleProfile)
	if err != nil {
		return "", err
	}
	req := types.ModelRequest{
		Messages: []types.Message{{
			Role: types.RoleUser,
			Blocks: append([]types.Block{types.Text{
				BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginSystem}},
				Text:      titleInstruction,
			}}, blocks...),
		}},
		Metadata: types.RequestMetadata{CostTags: types.CostTags{Feature: titleCostFeature}},
	}
	var text strings.Builder
	for chunk, cerr := range rc.Call(ctx, req) {
		if cerr != nil {
			return "", cerr
		}
		if chunk.Kind == types.DeltaText {
			text.WriteString(chunk.Delta)
		}
	}
	var title string
	if err := json.Unmarshal([]byte(strings.TrimSpace(text.String())), &title); err != nil {
		return "", err
	}
	return clipTitle(title), nil
}

// titleInput flattens the conversation into the blocks the recipe reads.
func titleInput(msgs []types.Message) []types.Block {
	var out []types.Block
	for _, m := range msgs {
		out = append(out, m.Blocks...)
	}
	return out
}

// clipTitle enforces the six-word ceiling the recipe promises.
func clipTitle(s string) string {
	words := strings.Fields(s)
	if len(words) <= 6 {
		return strings.TrimSpace(s)
	}
	return strings.Join(words[:6], " ")
}
