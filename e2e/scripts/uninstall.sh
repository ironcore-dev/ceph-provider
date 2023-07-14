#!/bin/bash

echo "Removing Minikube..."
minikube delete


echo "Removing Kubectl..."
sudo rm /usr/bin/kubectl

echo "Removing Go..."
sudo rm -rf /usr/local/go
