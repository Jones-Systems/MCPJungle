package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/pkg/types"
)

func (s *Server) registrationCapabilitiesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.mcpService.RegistrationContractAvailable() {
			c.JSON(http.StatusOK, gin.H{"registration_contract": 0})
			return
		}
		c.JSON(http.StatusOK, gin.H{"registration_contract": 1, "registration_deadline_seconds": 45, "registration_resolve_deadline_seconds": 50})
	}
}
func registrationContractError(c *gin.Context, err error) {
	var conflict *mcp.RegistrationConflict
	if errors.As(err, &conflict) {
		c.JSON(http.StatusConflict, gin.H{"error": conflict.Code})
		return
	}
	handleServiceError(c, err)
}

func registrationInput(c *gin.Context, resolve bool) (*types.RegisterServerInput, error) {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	input, _, err := mcp.DecodeRegistrationJSON(data, resolve)
	return input, err
}
func (s *Server) registerManagedServer(c *gin.Context) {
	force, err := parseForceQueryParam(c)
	if err != nil || force {
		c.JSON(http.StatusBadRequest, gin.H{"error": "registration contract does not support force"})
		return
	}
	input, err := registrationInput(c, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	server, err := createServerModelFromInput(input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err = s.mcpService.RegisterManagedMcpServer(c.Request.Context(), input, server); err != nil {
		registrationContractError(c, err)
		return
	}
	c.JSON(http.StatusCreated, types.RegisterServerResult{Server: &types.McpServer{Name: server.Name, Transport: string(server.Transport), Enabled: server.Enabled, Description: server.Description, SessionMode: string(server.SessionMode), Command: input.Command, Args: input.Args, Env: input.Env}})
}
func (s *Server) resolveRegistrationHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		input, err := registrationInput(c, true)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		outcome, err := s.mcpService.ResolveRegistration(c.Request.Context(), c.Param("name"), input)
		if err != nil {
			registrationContractError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"registration_contract": 1, "name": c.Param("name"), "outcome": outcome, "terminal": true})
	}
}
