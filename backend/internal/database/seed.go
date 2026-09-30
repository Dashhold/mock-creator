package database

import (
	"encoding/json"
	"fmt"
	"log"

	"mockcreator/internal/models"

	"gorm.io/gorm"
)

// seedSubject is a starter catalogue entry.
type seedSubject struct {
	Name    string
	Code    string
	Aliases []string
}

// starterCatalogue is the vocabulary the section detector starts with.
//
// This is recognition data, not exam knowledge: it maps headings that appear in
// real documents onto subjects. No exam is described here, and every row is
// editable from the Taxonomy page. Seeding only happens when the subjects table
// is empty, so user edits are never overwritten.
//
// Aliases matter because the same subject is printed differently by every
// paper setter. Matching against this list is what lets one parser handle
// documents from any exam without code changes.
var starterCatalogue = []seedSubject{
	{
		Name: "Quantitative Aptitude", Code: "quant",
		Aliases: []string{
			"quantitative aptitude", "quantitative ability", "quantitative techniques",
			"numerical ability", "numerical aptitude", "mathematics", "maths", "math",
			"arithmetic", "arithmetical ability", "elementary mathematics",
			"quantitative analysis", "data interpretation", "numeracy",
		},
	},
	{
		Name: "Reasoning", Code: "reasoning",
		Aliases: []string{
			"reasoning", "reasoning ability", "logical reasoning", "verbal reasoning",
			"non verbal reasoning", "non-verbal reasoning", "general intelligence",
			"general intelligence and reasoning", "general intelligence & reasoning",
			"mental ability", "analytical ability", "logical ability", "aptitude",
			"analytical reasoning", "logic",
		},
	},
	{
		Name: "English", Code: "english",
		Aliases: []string{
			"english", "english language", "english comprehension",
			"english language and comprehension", "english language & comprehension",
			"verbal ability", "language comprehension", "reading comprehension",
			"english grammar", "general english",
		},
	},
	{
		Name: "General Knowledge", Code: "gk",
		Aliases: []string{
			"general knowledge", "general awareness", "general studies", "gk", "ga",
			"current affairs", "general awareness and current affairs",
			"static gk", "miscellaneous", "general awareness & current affairs",
		},
	},
	{
		Name: "General Science", Code: "science",
		Aliases: []string{
			"general science", "science", "basic science", "everyday science",
			"science and technology", "science & technology",
		},
	},
	{
		Name: "Physics", Code: "physics",
		Aliases: []string{"physics", "applied physics"},
	},
	{
		Name: "Chemistry", Code: "chemistry",
		Aliases: []string{"chemistry", "applied chemistry", "organic chemistry", "inorganic chemistry"},
	},
	{
		Name: "Biology", Code: "biology",
		Aliases: []string{"biology", "life science", "life sciences", "botany", "zoology"},
	},
	{
		Name: "Computer Awareness", Code: "computer",
		Aliases: []string{
			"computer", "computers", "computer awareness", "computer knowledge",
			"computer fundamentals", "computer aptitude", "information technology",
			"basic computer knowledge", "digital literacy",
		},
	},
	{
		Name: "History", Code: "history",
		Aliases: []string{"history", "ancient history", "medieval history", "modern history", "world history"},
	},
	{
		Name: "Geography", Code: "geography",
		Aliases: []string{"geography", "physical geography", "world geography", "indian geography"},
	},
	{
		Name: "Polity and Governance", Code: "polity",
		Aliases: []string{
			"polity", "political science", "civics", "constitution", "governance",
			"indian polity", "polity and governance", "public administration",
		},
	},
	{
		Name: "Economics", Code: "economics",
		Aliases: []string{"economics", "economy", "indian economy", "economics and finance", "commerce"},
	},
	{
		Name: "Environment", Code: "environment",
		Aliases: []string{"environment", "ecology", "environmental science", "environment and ecology"},
	},
}

// Seed installs the starter subject catalogue when none exists. It is
// idempotent and never modifies rows the user has touched.
func Seed(db *gorm.DB) error {
	var count int64
	if err := db.Model(&models.Subject{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count subjects: %w", err)
	}
	if count > 0 {
		return nil
	}

	subjects := make([]models.Subject, 0, len(starterCatalogue))
	for _, s := range starterCatalogue {
		aliases, err := json.Marshal(s.Aliases)
		if err != nil {
			return fmt.Errorf("marshal aliases for %s: %w", s.Code, err)
		}
		subjects = append(subjects, models.Subject{
			Name:    s.Name,
			Code:    s.Code,
			Aliases: aliases,
			Description: "Starter catalogue entry. Edit the name, code and aliases " +
				"to match the wording your documents actually use.",
		})
	}

	if err := db.Create(&subjects).Error; err != nil {
		return fmt.Errorf("seed subjects: %w", err)
	}
	log.Printf("seeded %d starter subjects (editable from the Taxonomy page)", len(subjects))
	return nil
}
