package filter_expression_go_proto

type ExpressionCase int

const (
	Expression_NOT_SET ExpressionCase = iota
	Expression_Value_case
	Expression_Conjunction_case
	Expression_Disjunction_case
	Expression_Negation_case
	Expression_Restriction_case
	Expression_Function_case
	Expression_Phrase_case
	Expression_Member_case
)

func (x *Expression) WhichExpression() ExpressionCase {
	if x == nil {
		return Expression_NOT_SET
	}
	switch x.Expression.(type) {
	case *Expression_Value:
		return Expression_Value_case
	case *Expression_Conjunction:
		return Expression_Conjunction_case
	case *Expression_Disjunction:
		return Expression_Disjunction_case
	case *Expression_Negation:
		return Expression_Negation_case
	case *Expression_Restriction:
		return Expression_Restriction_case
	case *Expression_Function:
		return Expression_Function_case
	case *Expression_Phrase:
		return Expression_Phrase_case
	case *Expression_Member:
		return Expression_Member_case
	}
	return Expression_NOT_SET
}

type ExpressionParserOptions_builder struct {
	RejectLegacyOperatorPrecedence bool
	EnableRegexpMatching          bool
	EnableSimpleQuotedStrings      bool
	EnforceValidStrings            bool
}

func (b ExpressionParserOptions_builder) Build() *ExpressionParserOptions {
	return &ExpressionParserOptions{
		RejectLegacyOperatorPrecedence: b.RejectLegacyOperatorPrecedence,
		EnableRegexpMatching:          b.EnableRegexpMatching,
		EnableSimpleQuotedStrings:      b.EnableSimpleQuotedStrings,
		EnforceValidStrings:            b.EnforceValidStrings,
	}
}

type Expression_builder struct {
	Value       *string
	Conjunction *ExpressionList
	Disjunction *ExpressionList
	Negation    *Expression
	Restriction *Restriction
	Function    *Function
	Phrase      *Phrase
	Member      *ExpressionList
}

func (b Expression_builder) Build() *Expression {
	res := &Expression{}
	if b.Value != nil {
		res.Expression = &Expression_Value{Value: *b.Value}
	} else if b.Conjunction != nil {
		res.Expression = &Expression_Conjunction{Conjunction: b.Conjunction}
	} else if b.Disjunction != nil {
		res.Expression = &Expression_Disjunction{Disjunction: b.Disjunction}
	} else if b.Negation != nil {
		res.Expression = &Expression_Negation{Negation: b.Negation}
	} else if b.Restriction != nil {
		res.Expression = &Expression_Restriction{Restriction: b.Restriction}
	} else if b.Function != nil {
		res.Expression = &Expression_Function{Function: b.Function}
	} else if b.Phrase != nil {
		res.Expression = &Expression_Phrase{Phrase: b.Phrase}
	} else if b.Member != nil {
		res.Expression = &Expression_Member{Member: b.Member}
	}
	return res
}

type Restriction_builder struct {
	Lhs        *Expression
	Comparator Restriction_Comparator
	Rhs        *Expression
}

func (b Restriction_builder) Build() *Restriction {
	return &Restriction{
		Lhs:        b.Lhs,
		Comparator: b.Comparator,
		Rhs:        b.Rhs,
	}
}

type Phrase_builder struct {
	Values []string
}

func (b Phrase_builder) Build() *Phrase {
	return &Phrase{
		Values: b.Values,
	}
}

type ExpressionList_builder struct {
	Expressions []*Expression
}

func (b ExpressionList_builder) Build() *ExpressionList {
	return &ExpressionList{
		Expressions: b.Expressions,
	}
}



