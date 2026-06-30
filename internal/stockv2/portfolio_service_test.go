package stockv2

import (
	"context"
	"testing"
)

func TestCreatePortfolioDefaultsOmittedAgentPermissions(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	portfolio, err := svc.CreatePortfolio(context.Background(), RequestCreatePortfolio{
		Name:      "默认权限组合",
		Cash:      10000,
		RiskLevel: "medium",
	})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if !portfolio.AllowBuy || !portfolio.AllowAdd || !portfolio.AllowReduce || !portfolio.AllowSell {
		t.Fatalf("permissions = buy:%v add:%v reduce:%v sell:%v, want all true", portfolio.AllowBuy, portfolio.AllowAdd, portfolio.AllowReduce, portfolio.AllowSell)
	}
}

func TestCreatePortfolioKeepsExplicitAgentPermissions(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	deny := false

	portfolio, err := svc.CreatePortfolio(context.Background(), RequestCreatePortfolio{
		Name:      "只观察组合",
		Cash:      10000,
		RiskLevel: "medium",
		AllowBuy:  &deny,
		AllowAdd:  &deny,
	})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if portfolio.AllowBuy || portfolio.AllowAdd {
		t.Fatalf("explicit disabled permissions = buy:%v add:%v, want false", portfolio.AllowBuy, portfolio.AllowAdd)
	}
	if !portfolio.AllowReduce || !portfolio.AllowSell {
		t.Fatalf("omitted permissions = reduce:%v sell:%v, want true", portfolio.AllowReduce, portfolio.AllowSell)
	}
}
