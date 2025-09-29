package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var topicCmd = &cobra.Command{
	Use:   "topic",
	Short: "Topic management commands",
	Long:  "Commands for managing topic subscriptions and sending messages",
}

var listTopicsCmd = &cobra.Command{
	Use:   "list",
	Short: "List subscribed topics",
	Long:  "List all topics this node is currently subscribed to",
	Run: func(cmd *cobra.Command, args []string) {
		if err := peerManager.Start(); err != nil {
			logger.Error(fmt.Sprintf("Failed to start peer manager: %v", err), "cli")
			os.Exit(1)
		}
		defer peerManager.Stop()

		topics := peerManager.GetTopics()
		if len(topics) == 0 {
			fmt.Println("No topics subscribed")
			return
		}

		fmt.Println("Subscribed topics:")
		for _, topic := range topics {
			peers, err := peerManager.GetTopicPeers(topic)
			if err != nil {
				fmt.Printf("- %s (error getting peer count: %v)\n", topic, err)
			} else {
				fmt.Printf("- %s (%d peers)\n", topic, len(peers))
			}
		}
	},
}

var subscribeCmd = &cobra.Command{
	Use:   "subscribe <topic>",
	Short: "Subscribe to a topic",
	Long:  "Subscribe to a topic to receive messages from other peers",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		topic := args[0]

		if err := peerManager.Start(); err != nil {
			logger.Error(fmt.Sprintf("Failed to start peer manager: %v", err), "cli")
			os.Exit(1)
		}

		if err := peerManager.SubscribeToTopic(topic); err != nil {
			logger.Error(fmt.Sprintf("Failed to subscribe to topic '%s': %v", topic, err), "cli")
			peerManager.Stop()
			os.Exit(1)
		}

		fmt.Printf("Successfully subscribed to topic: %s\n", topic)
		logger.Info(fmt.Sprintf("Subscribed to topic: %s", topic), "cli")

		// Keep running to maintain subscription
		fmt.Println("Maintaining subscription... Press Ctrl+C to stop.")
		select {} // Block forever
	},
}

var sendCmd = &cobra.Command{
	Use:   "send <topic> <message>",
	Short: "Send a message to a topic",
	Long:  "Send a message to all peers subscribed to the specified topic",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		topic := args[0]
		message := args[1]

		if err := peerManager.Start(); err != nil {
			logger.Error(fmt.Sprintf("Failed to start peer manager: %v", err), "cli")
			os.Exit(1)
		}
		defer peerManager.Stop()

		// First subscribe to the topic if not already subscribed
		topics := peerManager.GetTopics()
		subscribed := false
		for _, t := range topics {
			if t == topic {
				subscribed = true
				break
			}
		}

		if !subscribed {
			if err := peerManager.SubscribeToTopic(topic); err != nil {
				logger.Error(fmt.Sprintf("Failed to subscribe to topic '%s': %v", topic, err), "cli")
				os.Exit(1)
			}
			fmt.Printf("Auto-subscribed to topic: %s\n", topic)
		}

		// Send the message
		if err := peerManager.SendMessageToTopic(topic, []byte(message)); err != nil {
			logger.Error(fmt.Sprintf("Failed to send message to topic '%s': %v", topic, err), "cli")
			os.Exit(1)
		}

		peers, _ := peerManager.GetTopicPeers(topic)
		fmt.Printf("Message sent to %d peers in topic '%s'\n", len(peers), topic)
		logger.Info(fmt.Sprintf("Sent message to topic '%s': %s", topic, message), "cli")
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show node status",
	Long:  "Show detailed status of the node including topics and peer counts",
	Run: func(cmd *cobra.Command, args []string) {
		if err := peerManager.Start(); err != nil {
			logger.Error(fmt.Sprintf("Failed to start peer manager: %v", err), "cli")
			os.Exit(1)
		}
		defer peerManager.Stop()

		stats := peerManager.GetStats()

		// Pretty print the stats
		prettyStats, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			fmt.Printf("Error formatting stats: %v\n", err)
			return
		}

		fmt.Println("Node Status:")
		fmt.Println(string(prettyStats))
	},
}

func init() {
	rootCmd.AddCommand(topicCmd)
	topicCmd.AddCommand(listTopicsCmd)
	topicCmd.AddCommand(subscribeCmd)
	topicCmd.AddCommand(sendCmd)
	topicCmd.AddCommand(statusCmd)
}