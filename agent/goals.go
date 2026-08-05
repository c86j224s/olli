package agent

import (
	"fmt"
	"strings"

	"github.com/c86j224s/olli/ollama"
)

func (a *Agent) registerGoalTools() {
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "set_active_goal",
			Description: "Set or update the agent's active goal to stay focused on achieving a multi-step objective",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"goal_description": {
						Type:        "string",
						Description: "Description of the objective or goal to accomplish",
					},
				},
				Required: []string{"goal_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		g, ok := args["goal_description"].(string)
		if !ok || g == "" {
			return "", fmt.Errorf("invalid goal description")
		}
		a.SetGoal(g)
		return fmt.Sprintf("Active Goal set to: '%s'", g), nil
	})

	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "complete_goal",
			Description: "Mark the active goal as completed and clear the goal state after achieving all steps",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"completion_summary": {
						Type:        "string",
						Description: "Summary of achievements and completed goal results",
					},
				},
				Required: []string{"completion_summary"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		summary, _ := args["completion_summary"].(string)
		prevGoal := a.activeGoal
		a.ClearGoal()
		return fmt.Sprintf("🎉 Goal '%s' marked as COMPLETED! Summary: %s", prevGoal, summary), nil
	})
}

func (a *Agent) SetGoal(goal string) {
	a.activeGoal = strings.TrimSpace(goal)
}

func (a *Agent) ClearGoal() {
	a.activeGoal = ""
}

func (a *Agent) GetGoal() string {
	return a.activeGoal
}

func (a *Agent) IsGoalActive() bool {
	return a.activeGoal != ""
}
